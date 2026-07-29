package wimlib

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompressionConstants(t *testing.T) {
	assert.Equal(t, Compression(1), LZX)
	assert.Equal(t, Compression(2), LZMS)
	assert.Equal(t, Compression(0), None)
}

func TestAvailable(t *testing.T) {
	_ = Available()
}

func TestOpenWIM_NonexistentFile(t *testing.T) {
	_, err := OpenWIM("/nonexistent/path.wim")
	assert.Error(t, err)
}

func TestCreateWIM_Stub(t *testing.T) {
	if Available() {
		t.Skip("wimlib is available — stub tests not applicable")
	}
	_, err := CreateWIM(LZX)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestOpenWIM_Stub(t *testing.T) {
	if Available() {
		t.Skip("wimlib is available — stub tests not applicable")
	}
	_, err := OpenWIM("/some/file.wim")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestWIM_UpdateImageAdd_Stub(t *testing.T) {
	if Available() {
		t.Skip("wimlib is available — stub tests not applicable")
	}
	w := &WIM{}
	err := w.UpdateImageAdd(1, "/tmp/src", "/dest")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestWIM_UpdateImageAddTree_Stub(t *testing.T) {
	if Available() {
		t.Skip("wimlib is available — stub tests not applicable")
	}
	w := &WIM{}
	err := w.UpdateImageAddTree(1, "/tmp/dir", "/dest")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestWIM_UpdateImageDelete_Stub(t *testing.T) {
	if Available() {
		t.Skip("wimlib is available — stub tests not applicable")
	}
	w := &WIM{}
	err := w.UpdateImageDelete(1, "/some/path")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestWIM_SetImageProperty_Stub(t *testing.T) {
	if Available() {
		t.Skip("wimlib is available — stub tests not applicable")
	}
	w := &WIM{}
	err := w.SetImageProperty(1, "FLAGS", "2")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestWIM_SetImageName_Stub(t *testing.T) {
	if Available() {
		t.Skip("wimlib is available — stub tests not applicable")
	}
	w := &WIM{}
	err := w.SetImageName(1, "Test Name", "Test Description")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}
