package sample

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/antongulenko/golib"
)

const (
	tcp_sample_buffer = 50
)

// ==================== TCP write connection ====================
type tcpWriteConn struct {
	sink    *tcpMetricSink
	remote  net.Addr
	conn    *net.TCPConn
	samples chan Sample
}

type tcpMetricSink struct {
	AbstractMarshallingMetricSink
	LastHeader Header
}

func (sink *tcpMetricSink) writeConn(conn *net.TCPConn) *tcpWriteConn {
	return &tcpWriteConn{
		sink:    sink,
		conn:    conn,
		remote:  conn.RemoteAddr(),
		samples: make(chan Sample, tcp_sample_buffer),
	}
}

func (conn *tcpWriteConn) Close() {
	if conn != nil {
		conn.err(nil)
		if samples := conn.samples; samples != nil {
			conn.samples = nil
			close(samples)
		}
	}
}

func (conn *tcpWriteConn) Running() bool {
	return conn != nil && conn.conn != nil
}

func (conn *tcpWriteConn) err(err error) {
	if connection := conn.conn; connection != nil {
		conn.conn = nil
		if operr, ok := err.(*net.OpError); ok && operr.Err == syscall.EPIPE {
			log.Printf("Connection to %v closed\n", conn.remote)
		} else if err != nil {
			log.Printf("TCP write to %v failed, closing connection. %v\n", conn.remote, err)
		} else {
			log.Println("Closing connection to", conn.remote)
		}
		if err := connection.Close(); err != nil {
			log.Printf("Error closing connection to %v: %v\n", conn.remote, err)
		}
	}
}

func (conn *tcpWriteConn) Run(wg *sync.WaitGroup) {
	defer func() {
		conn.conn = nil // In case of panic, avoid full channel-buffer
		wg.Done()
	}()
	log.Println("Serving", len(conn.sink.LastHeader.Fields), "metrics to", conn.remote)
	if err := conn.sink.Marshaller.WriteHeader(conn.sink.LastHeader, conn.conn); err != nil {
		conn.err(err)
		return
	}
	for sample := range conn.samples {
		connection := conn.conn
		if connection == nil {
			break
		}
		if err := conn.sink.Marshaller.WriteSample(sample, conn.sink.LastHeader, connection); err != nil {
			conn.err(err)
			break
		}
	}
}

// ==================== TCP active sink ====================
type TCPSink struct {
	tcpMetricSink
	Endpoint string
	wg       *sync.WaitGroup
	conn     *tcpWriteConn
	stopped  *golib.OneshotCondition
}

func (sink *TCPSink) String() string {
	return "TCP sink to " + sink.Endpoint
}

func (sink *TCPSink) Start(wg *sync.WaitGroup) golib.StopChan {
	log.Println("Sending", sink.Marshaller, "samples to", sink.Endpoint)
	sink.stopped = golib.NewOneshotCondition()
	sink.wg = wg
	return sink.stopped.Start(wg)
}

func (sink *TCPSink) closeConnection() {
	sink.conn.Close()
	sink.conn = nil
}

func (sink *TCPSink) Close() {
	sink.stopped.Enable(func() {
		sink.closeConnection()
	})
}

func (sink *TCPSink) Header(header Header) (err error) {
	sink.stopped.IfElseEnabled(func() {
		err = fmt.Errorf("TCP sink to %v already stopped", sink.Endpoint)
	}, func() {
		sink.closeConnection() // Stop existing connection to negotiate new header
		sink.LastHeader = header
		err = sink.assertConnection()
	})
	return
}

func (sink *TCPSink) Sample(sample Sample, header Header) (err error) {
	sink.stopped.IfElseEnabled(func() {
		err = fmt.Errorf("TCP sink to %v already stopped", sink.Endpoint)
	}, func() {
		if err = sample.Check(header); err != nil {
			return
		}
		if !sink.conn.Running() {
			sink.closeConnection() // Cleanup errored connection
		}
		if err = sink.assertConnection(); err != nil {
			return
		}
		sink.conn.samples <- sample
	})
	return
}

func (sink *TCPSink) assertConnection() error {
	if sink.conn == nil {
		endpoint, err := net.ResolveTCPAddr("tcp", sink.Endpoint)
		if err != nil {
			return err
		}
		conn, err := net.DialTCP("tcp", nil, endpoint)
		if err != nil {
			return err
		}
		sink.conn = sink.writeConn(conn)
		sink.wg.Add(1)
		go sink.conn.Run(sink.wg)
	}
	return nil
}

// ==================== TCP active source ====================
type TCPSource struct {
	AbstractUnmarshallingMetricSource
	RemoteAddr    string
	RetryInterval time.Duration
	loopTask      *golib.LoopTask
	conn          *net.TCPConn
}

func (sink *TCPSource) String() string {
	return "TCP source from " + sink.RemoteAddr
}

func (source *TCPSource) Start(wg *sync.WaitGroup) golib.StopChan {
	log.Println("Downloading", source.Unmarshaller, "data from", source.RemoteAddr)
	source.loopTask = golib.NewLoopTask("tcp download source", func(stop golib.StopChan) {
		if conn, err := source.dial(); err != nil {
			log.Println("Error downloading data:", err)
		} else {
			source.loopTask.IfElseEnabled(func() {
				return
			}, func() {
				source.conn = conn
			})
			tcpReadSamples(conn, source.Unmarshaller, source.OutgoingSink, source.connectionClosed)
		}
		select {
		case <-time.After(source.RetryInterval):
		case <-stop:
		}
	})
	source.loopTask.StopHook = source.loopStopped
	return source.loopTask.Start(wg)
}

func (source *TCPSource) Stop() {
	source.loopTask.Enable(func() {
		if conn := source.conn; conn != nil {
			_ = conn.Close() // Ignore error
		}
	})
}

func (source *TCPSource) loopStopped() {
	source.CloseSink()
}

func (source *TCPSource) connectionClosed() bool {
	return source.loopTask.Enabled()
}

func (source *TCPSource) dial() (*net.TCPConn, error) {
	endpoint, err := net.ResolveTCPAddr("tcp", source.RemoteAddr)
	if err != nil {
		return nil, err
	}
	return net.DialTCP("tcp", nil, endpoint)
}

func tcpReadSamples(conn *net.TCPConn, um Unmarshaller, sink MetricSink, checkClosed func() bool) {
	log.Println("Receiving header from", conn.RemoteAddr())
	var err error
	var num_samples int
	if num_samples, err = readSamples(conn, um, sink); err == io.EOF {
		log.Println("Connection closed by", conn.RemoteAddr())
	} else if checkClosed() {
		log.Println("Connection to", conn.RemoteAddr(), "closed")
	} else if err != nil {
		log.Printf("Error receiving samples from %v: %v\n", conn.RemoteAddr(), err)
		_ = conn.Close() // Ignore error
	}
	log.Println("Received", num_samples, "samples from", conn.RemoteAddr())
}