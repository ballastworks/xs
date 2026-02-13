package xi_io

import (
	"io"
	"sync"
)

type WriterToReader interface {
	io.Reader
	io.WriterTo
}

var NopCloserWriterToReaderPool = sync.Pool{
	New: func() any {
		return &NopCloserWriterToReader{}
	},
}

var NopCloserPool = sync.Pool{
	New: func() any {
		return &NopCloser{}
	},
}

type NopCloser struct {
	io.Reader
}

func (*NopCloser) Close() error { return nil }

func (*NopCloser) IsNopCloser() bool { return true }

type NopCloserWriterToReader struct {
	WriterToReader
}

func (*NopCloserWriterToReader) Close() error { return nil }

func (*NopCloserWriterToReader) IsNopCloser() bool { return true }

func ReleaseNopCloser(rc io.Reader) {
	switch v := rc.(type) {
	case *NopCloserWriterToReader:
		*v = NopCloserWriterToReader{}
		NopCloserWriterToReaderPool.Put(v)
	case *NopCloser:
		*v = NopCloser{}
		NopCloserPool.Put(v)
	}
}
