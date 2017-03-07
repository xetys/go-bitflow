package bitflow

import (
	"bufio"
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/suite"
)

type MarshallerTestSuite struct {
	testSuiteWithSamples
}

func TestMarshallerTestSuite(t *testing.T) {
	suite.Run(t, new(MarshallerTestSuite))
}

func (suite *MarshallerTestSuite) testRead(m BidiMarshaller, rdr *bufio.Reader, expectedHeader *Header, samples []*Sample) {
	header, data, err := m.Read(rdr, nil)
	suite.NoError(err)
	suite.Nil(data)
	suite.NotNil(header)
	suite.compareHeaders(expectedHeader, header)

	for _, expectedSample := range samples {
		nilHeader, data, err := m.Read(rdr, header)
		suite.NoError(err)
		suite.NotNil(data)
		suite.Nil(nilHeader)
		capacity := 0
		if len(expectedSample.Values) > 0 {
			capacity = len(expectedSample.Values) + 3
		}
		sample, err := m.ParseSample(header, capacity, data)
		suite.NoError(err)
		suite.compareSamples(expectedSample, sample, capacity)
	}
}

func (suite *MarshallerTestSuite) write(m BidiMarshaller, buf *bytes.Buffer, header *Header, samples []*Sample) {
	suite.NoError(m.WriteHeader(header, buf))
	for _, sample := range samples {
		suite.NoError(m.WriteSample(sample, header, buf))
	}
}

func (suite *MarshallerTestSuite) testAllHeaders(m BidiMarshaller) {
	var buf bytes.Buffer
	for i, header := range suite.headers {
		suite.write(m, &buf, header, suite.samples[i])
	}

	counter := &countingBuf{data: buf.Bytes()}
	rdr := bufio.NewReader(counter)
	for i, header := range suite.headers {
		suite.testRead(m, rdr, header, suite.samples[i])
	}
	suite.Equal(0, len(counter.data))
	_, err := rdr.ReadByte()
	suite.Error(err)
}

func (suite *MarshallerTestSuite) testIndividualHeaders(m BidiMarshaller) {
	for i, header := range suite.headers {
		var buf bytes.Buffer
		suite.write(m, &buf, header, suite.samples[i])

		counter := &countingBuf{data: buf.Bytes()}
		rdr := bufio.NewReader(counter)
		suite.testRead(m, rdr, header, suite.samples[i])
		suite.Equal(0, len(counter.data))
		_, err := rdr.ReadByte()
		suite.Error(err)
	}
}

func (suite *MarshallerTestSuite) TestCsvMarshallerSingle() {
	suite.testIndividualHeaders(new(CsvMarshaller))
}

func (suite *MarshallerTestSuite) TestCsvMarshallerMulti() {
	suite.testAllHeaders(new(CsvMarshaller))
}

func (suite *MarshallerTestSuite) TestBinaryMarshallerSingle() {
	suite.testIndividualHeaders(new(BinaryMarshaller))
}

func (suite *MarshallerTestSuite) TestBinaryMarshallerMulti() {
	suite.testAllHeaders(new(BinaryMarshaller))
}

type failingBuf struct {
	err error
}

func (c *failingBuf) Read(b []byte) (int, error) {
	return copy(b, []byte{'x'}), c.err
}

func (suite *MarshallerTestSuite) testEOF(m Unmarshaller) {
	buf := failingBuf{err: io.EOF}
	rdr := bufio.NewReader(&buf)
	header, data, err := m.Read(rdr, nil)
	suite.Nil(header)
	suite.Nil(data)
	suite.Equal(io.ErrUnexpectedEOF, err)
}

func (suite *MarshallerTestSuite) TestCsvEOF() {
	suite.testEOF(new(CsvMarshaller))
}

func (suite *MarshallerTestSuite) TestBinaryEOF() {
	suite.testEOF(new(BinaryMarshaller))
}
