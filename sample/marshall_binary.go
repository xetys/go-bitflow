package sample

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

const (
	timeBytes        = 8
	valBytes         = 8
	binary_separator = '\n'
)

type BinaryMarshaller struct {
}

func (*BinaryMarshaller) String() string {
	return "binary"
}

func (*BinaryMarshaller) WriteHeader(header Header, writer io.Writer) error {
	w := WriteCascade{Writer: writer}
	w.WriteStr(time_col)
	w.WriteByte(binary_separator)
	if header.HasTags {
		w.WriteStr(tags_col)
		w.WriteByte(binary_separator)
	}
	for _, name := range header.Fields {
		w.WriteStr(name)
		w.WriteByte(binary_separator)
	}
	w.WriteByte(binary_separator)
	return w.Err
}

func (*BinaryMarshaller) ReadHeader(reader *bufio.Reader) (header Header, err error) {
	name, err := reader.ReadBytes(binary_separator)
	if err != nil {
		return
	}
	if err = checkFirstCol(string(name[:len(name)-1])); err != nil {
		return
	}

	first := true
	for {
		var nameBytes []byte
		nameBytes, err = reader.ReadBytes(binary_separator)
		if err != nil {
			return
		}
		if len(nameBytes) <= 1 {
			return
		}
		name := string(nameBytes[:len(nameBytes)-1])
		if first && name == tags_col {
			header.HasTags = true
		} else {
			header.Fields = append(header.Fields, name)
		}
		first = false
	}
}

func (m *BinaryMarshaller) WriteSample(sample Sample, header Header, writer io.Writer) error {
	// Time as big-endian uint64 nanoseconds since Unix epoch
	tim := make([]byte, timeBytes)
	binary.BigEndian.PutUint64(tim, uint64(sample.Time.UnixNano()))
	if _, err := writer.Write(tim); err != nil {
		return err
	}

	// Tags
	if header.HasTags {
		str := sample.TagString()
		if _, err := writer.Write([]byte(str)); err != nil {
			return err
		}
		if _, err := writer.Write([]byte{binary_separator}); err != nil {
			return err
		}
	}

	// Values as big-endian double precision
	for _, value := range sample.Values {
		valBits := math.Float64bits(float64(value))
		val := make([]byte, valBytes)
		binary.BigEndian.PutUint64(val, valBits)
		if _, err := writer.Write(val); err != nil {
			return err
		}
	}
	return nil
}

func (*BinaryMarshaller) ReadSampleData(header Header, input *bufio.Reader) ([]byte, error) {
	valuelen := valBytes * len(header.Fields)
	minlen := timeBytes + valuelen
	data := make([]byte, minlen)
	_, err := io.ReadFull(input, data) // Can be io.EOF
	if err != nil {
		return nil, err
	}
	if !header.HasTags {
		return data, nil
	} else {
		index := bytes.IndexByte(data[timeBytes:], binary_separator)
		if index >= 0 {
			result := make([]byte, minlen+index+1)
			copy(result, data)
			_, err := io.ReadFull(input, result[minlen:])
			return result, unexpectedEOF(err)
		} else {
			tagRest, err := input.ReadBytes(binary_separator)
			if err != nil {
				return nil, unexpectedEOF(err)
			}
			result := make([]byte, minlen+len(tagRest)+valuelen)
			_, err = io.ReadFull(input, result[minlen+len(tagRest):])
			if err != nil {
				return nil, unexpectedEOF(err)
			}
			copy(result, data)
			copy(result[minlen:], tagRest)
			return result, nil
		}
	}
}

func unexpectedEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func (*BinaryMarshaller) ParseSample(header Header, data []byte) (sample Sample, err error) {
	// Required size
	size := timeBytes + len(header.Fields)*valBytes
	if len(data) < size {
		err = fmt.Errorf("Data slice not long enough (%v < %v)", len(data), size)
		return
	}

	// Time
	timeVal := binary.BigEndian.Uint64(data[:timeBytes])
	data = data[timeBytes:]
	sample.Time = time.Unix(0, int64(timeVal))

	// Tags
	if header.HasTags {
		index := bytes.IndexByte(data, binary_separator)
		if index < 0 {
			err = errors.New("Binary sample data did not contain tag separator")
			return
		}
		size = index + 1 + len(header.Fields)*valBytes
		if len(data) != size {
			err = fmt.Errorf("Data slice wrong len (%v != %v)", len(data), size)
			return
		}
		if err = sample.ParseTagString(string(data[:index])); err != nil {
			return
		}
		data = data[index+1:]
	}

	// Values
	for i := 0; i < len(header.Fields); i++ {
		valBits := binary.BigEndian.Uint64(data[:valBytes])
		data = data[valBytes:]
		value := math.Float64frombits(valBits)
		sample.Values = append(sample.Values, Value(value))
	}
	return
}