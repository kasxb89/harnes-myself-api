package mediaprobe

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInspectActualPNGAndTruncatedPayload(t *testing.T) {
	var b bytes.Buffer
	m := image.NewNRGBA(image.Rect(0, 0, 24, 16))
	m.Set(2, 3, color.NRGBA{R: 40, G: 100, B: 200, A: 255})
	require.NoError(t, png.Encode(&b, m))
	p := filepath.Join(t.TempDir(), "image.bin")
	require.NoError(t, os.WriteFile(p, b.Bytes(), 0600))
	metadata, err := Inspect(t.Context(), p, b.Bytes())
	require.NoError(t, err)
	require.Equal(t, "image/png", metadata.MIMEType)
	require.Equal(t, 24, metadata.Width)
	require.Equal(t, 16, metadata.Height)
	_, err = Inspect(t.Context(), p, b.Bytes()[:40])
	require.Error(t, err)
	_, err = Inspect(t.Context(), p, []byte("<svg onload=evil>"))
	require.Error(t, err)
	file, err := os.OpenFile(p, os.O_RDWR, 0600)
	require.NoError(t, err)
	defer file.Close()
	_, err = InspectFile(t.Context(), file, int64(b.Len()-1))
	require.Error(t, err)
	require.NoError(t, file.Truncate(MaxBytes+1))
	_, err = InspectFile(t.Context(), file, MaxBytes)
	require.Error(t, err)
	metadata, err = InspectFile(t.Context(), file, MaxBytes+1)
	require.NoError(t, err, "a private large file is decoded without a whole-file byte slice")
	require.Equal(t, "image/png", metadata.MIMEType)
	require.Equal(t, 24, metadata.Width)
}
func TestInspectActualMP4AndWAVWithoutClientMeasurements(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe unavailable")
	}
	for _, kind := range []string{"video", "audio"} {
		t.Run(kind, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "asset.bin")
			args := []string{"-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=48x32:r=24:d=0.5", "-c:v", "mpeg4", "-f", "mp4", "-y", p}
			if kind == "audio" {
				args = []string{"-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.5", "-f", "wav", "-y", p}
			}
			require.NoError(t, exec.CommandContext(context.Background(), "ffmpeg", args...).Run())
			data, err := os.ReadFile(p)
			require.NoError(t, err)
			metadata, err := Inspect(t.Context(), p, data)
			require.NoError(t, err)
			require.Equal(t, kind, metadata.Kind)
			require.InDelta(t, 0.5, metadata.DurationSeconds, 0.05)
			if kind == "video" {
				require.Equal(t, 48, metadata.Width)
				require.Equal(t, 32, metadata.Height)
				require.InDelta(t, 24, metadata.FPS, 0.01)
				require.Equal(t, "mp4", metadata.Format)
			}
		})
	}
}
