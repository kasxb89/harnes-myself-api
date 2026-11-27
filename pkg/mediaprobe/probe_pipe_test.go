package mediaprobe

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectFeedsOnlyActualBytesToPipeAndDisablesExternalReferences(t *testing.T) {
	root := t.TempDir()
	arguments, input := filepath.Join(root, "arguments"), filepath.Join(root, "input")
	t.Setenv("MEDIA_PROBE_TEST_ARGS", arguments)
	t.Setenv("MEDIA_PROBE_TEST_INPUT", input)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ffprobe"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$MEDIA_PROBE_TEST_ARGS\"\ncat > \"$MEDIA_PROBE_TEST_INPUT\"\nprintf '%s' '{\"streams\":[{\"codec_type\":\"audio\",\"duration\":\"0.5\"}],\"format\":{\"format_name\":\"wav\",\"duration\":\"0.5\"}}'\n"), 0700))
	path := filepath.Join(root, "must-not-read.wav")
	require.NoError(t, os.WriteFile(path, []byte("different file contents"), 0600))
	content := []byte("actual uploaded bytes")
	_, err := Inspect(t.Context(), path, content)
	require.NoError(t, err)
	actual, err := os.ReadFile(input)
	require.NoError(t, err)
	assert.Equal(t, content, actual)
	args, err := os.ReadFile(arguments)
	require.NoError(t, err)
	assert.Contains(t, string(args), "-protocol_whitelist\npipe\n")
	assert.Contains(t, string(args), "-enable_drefs\n0\n-use_absolute_path\n0\n")
	assert.True(t, strings.HasSuffix(string(args), "pipe:0\n"))
	assert.NotContains(t, string(args), path)
	assert.NotContains(t, string(args), "file,pipe")
	require.NoError(t, os.WriteFile(path, content, 0600))
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	_, err = InspectFile(t.Context(), file, int64(len(content)))
	require.NoError(t, err)
	actual, err = os.ReadFile(input)
	require.NoError(t, err)
	assert.Equal(t, content, actual)
	args, err = os.ReadFile(arguments)
	require.NoError(t, err)
	assert.Contains(t, string(args), "-protocol_whitelist\npipe\n")
	assert.NotContains(t, string(args), path)
}

func TestInspectRejectsExternalPlaylistAndCanceledContext(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	content := []byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nfile:///private/not-owned/segment.ts\n#EXT-X-ENDLIST\n")
	_, err := Inspect(t.Context(), "", content)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = Inspect(ctx, "", content)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAudioPacketDurationAccumulatorIsBoundedAndUsesIntegerTicks(t *testing.T) {
	var w packetDurationWriter
	_, err := w.Write([]byte("4096\n409"))
	require.NoError(t, err)
	_, err = w.Write([]byte("6\n1"))
	require.NoError(t, err)
	require.NoError(t, w.finish())
	assert.Equal(t, int64(8193), w.total)
	for _, bad := range [][]byte{[]byte("N/A\n"), []byte("-1\n"), bytes.Repeat([]byte("9"), 129)} {
		var rejected packetDurationWriter
		_, err = rejected.Write(bad)
		require.Error(t, err)
	}
}
