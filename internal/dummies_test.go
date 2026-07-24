package internal

import (
	"io"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
)

func TestCreateDummyBlob(t *testing.T) {
	dummy, err := CreateDummyBlob(plumbing.NewHash("334cde09da4afcb74f8d2b3e6fd6cce61228b485"))
	assert.NoError(t, err)
	assert.Equal(t, "334cde09da4afcb74f8d2b3e6fd6cce61228b485", dummy.Hash.String())
	assert.Equal(t, int64(0), dummy.Size)
	reader, err := dummy.Reader()
	assert.NoError(t, err)
	buffer := make([]byte, 1)
	buffer[0] = 0xff
	n, err := reader.Read(buffer)
	assert.Equal(t, err, io.EOF)
	assert.Equal(t, 0, n)
	assert.Equal(t, buffer[0], byte(0xff))
	reader.Close()
}

func TestCreateDummyBlobFails(t *testing.T) {
	dummy, err := CreateDummyBlob(plumbing.NewHash("334cde09da4afcb74f8d2b3e6fd6cce61228b485"), true)
	assert.NoError(t, err)
	reader, err := dummy.Reader()
	assert.Nil(t, reader)
	assert.Error(t, err)
	assert.Panics(t, func() {
		CreateDummyBlob(plumbing.NewHash("334cde09da4afcb74f8d2b3e6fd6cce61228b485"), true, true)
	})
}

func TestNotUsedDummyStuff(t *testing.T) {
	dio := dummyIO{}
	n, err := dio.Write([]byte{})
	assert.NoError(t, err)
	assert.Equal(t, 0, n)
	obj := dummyEncodedObject{}
	obj.SetSize(int64(100))
	obj.SetType(plumbing.CommitObject)
	writer, err := obj.Writer()
	assert.NoError(t, err)
	assert.NotNil(t, writer)
}
