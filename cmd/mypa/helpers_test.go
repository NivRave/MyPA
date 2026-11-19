package main

import "io"

// repeatingReader generates a long stream of bytes without allocating a huge slice in memory.
// Shared by the body-limit tests of the edge, the ingress and the app router.
type repeatingReader struct {
	data      []byte
	bytesLeft int64
}

func newRepeatingReader(pattern string, totalBytes int64) *repeatingReader {
	return &repeatingReader{
		data:      []byte(pattern),
		bytesLeft: totalBytes,
	}
}

func (r *repeatingReader) Read(p []byte) (n int, err error) {
	if r.bytesLeft <= 0 {
		return 0, io.EOF
	}
	toRead := int64(len(p))
	if toRead > r.bytesLeft {
		toRead = r.bytesLeft
	}
	patternLen := int64(len(r.data))
	for i := int64(0); i < toRead; i++ {
		p[i] = r.data[i%patternLen]
	}
	r.bytesLeft -= toRead
	return int(toRead), nil
}
