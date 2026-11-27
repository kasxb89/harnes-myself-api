// Package mediaprobe measures uploaded bytes without trusting request metadata.
package mediaprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

const MaxBytes = 50 << 20

var ErrUnsupported = errors.New("unsupported or invalid media content")
var ErrProbeUnavailable = errors.New("media measurement is unavailable")

type Metadata struct {
	Kind, MIMEType, Format string
	Width, Height          int
	DurationSeconds, FPS   float64
}

func Inspect(ctx context.Context, _ string, content []byte) (Metadata, error) {
	return inspect(ctx, bytes.NewReader(content), int64(len(content)), MaxBytes)
}

// InspectFile measures a private regular file without loading it into memory.
// Only its bounded bytes reach the decoders and ffprobe's pipe; the file path
// never grants ffprobe permission to open external files or network URLs.
func InspectFile(ctx context.Context, file *os.File, maxBytes int64) (Metadata, error) {
	if file == nil {
		return Metadata{}, ErrUnsupported
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Metadata{}, ErrUnsupported
	}
	return inspect(ctx, file, info.Size(), maxBytes)
}

func inspect(ctx context.Context, content io.ReadSeeker, size, maxBytes int64) (Metadata, error) {
	var m Metadata
	if err := ctx.Err(); err != nil {
		return m, err
	}
	if size <= 0 || maxBytes <= 0 || size > maxBytes {
		return m, ErrUnsupported
	}
	reader, err := mediaProbeReader(ctx, content, size)
	if err != nil {
		return m, err
	}
	if cfg, format, err := image.DecodeConfig(reader); err == nil {
		mime := map[string]string{"jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp"}[format]
		if mime == "" || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 16384 || cfg.Height > 16384 || int64(cfg.Width)*int64(cfg.Height) > 50_000_000 {
			return m, ErrUnsupported
		}
		reader, err = mediaProbeReader(ctx, content, size)
		if err != nil {
			return m, err
		}
		decoded, actualFormat, decodeErr := image.Decode(reader)
		if decodeErr != nil || decoded == nil || actualFormat != format || decoded.Bounds().Dx() != cfg.Width || decoded.Bounds().Dy() != cfg.Height {
			return m, ErrUnsupported
		}
		return Metadata{Kind: "image", MIMEType: mime, Format: format, Width: cfg.Width, Height: cfg.Height}, nil
	}
	// Only the supplied bytes enter ffprobe. The legacy path argument is never
	// opened; external files, playlists, network protocols and MOV drefs cannot
	// expand the measured input. Context, output and allocation remain bounded.
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	reader, err = mediaProbeReader(bounded, content, size)
	if err != nil {
		return m, err
	}
	cmd := byteProbeCommand(bounded, reader, "-show_entries", "format=format_name,duration:stream=codec_type,codec_name,width,height,duration,avg_frame_rate,time_base", "-of", "json", "pipe:0")
	var out cappedWriter
	cmd.Stdout = &out
	cmd.Stderr = &discardWriter{}
	if err := cmd.Run(); err != nil {
		if bounded.Err() != nil {
			return m, bounded.Err()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return m, ErrProbeUnavailable
		}
		return m, ErrUnsupported
	}
	var p struct {
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			Type     string `json:"codec_type"`
			Codec    string `json:"codec_name"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Duration string `json:"duration"`
			FPS      string `json:"avg_frame_rate"`
			TimeBase string `json:"time_base"`
		} `json:"streams"`
	}
	if json.Unmarshal(out.Bytes(), &p) != nil || len(p.Streams) == 0 || len(p.Streams) > 32 {
		return m, ErrUnsupported
	}
	duration, _ := strconv.ParseFloat(p.Format.Duration, 64)
	audioTimeBase := float64(0)
	for _, s := range p.Streams {
		if s.Type == "video" && s.Width > 0 && s.Height > 0 {
			m.Kind = "video"
			m.Width = s.Width
			m.Height = s.Height
			m.FPS = parseRatio(s.FPS)
			if duration <= 0 {
				duration, _ = strconv.ParseFloat(s.Duration, 64)
			}
			break
		}
		if s.Type == "audio" && m.Kind == "" {
			m.Kind = "audio"
			audioTimeBase = parseRatio(s.TimeBase)
			if duration <= 0 {
				duration, _ = strconv.ParseFloat(s.Duration, 64)
			}
		}
	}
	// Non-seekable WAV/MP3 inputs do not expose a container duration. Count
	// actual audio packet ticks instead of trusting a client measurement or a
	// claimed file length. The streaming accumulator retains one bounded line.
	if m.Kind == "audio" && duration <= 0 && finite(audioTimeBase) && audioTimeBase > 0 {
		var ticks packetDurationWriter
		reader, err = mediaProbeReader(bounded, content, size)
		if err != nil {
			return m, err
		}
		packets := byteProbeCommand(bounded, reader, "-select_streams", "a:0", "-show_entries", "packet=duration", "-of", "csv=p=0", "pipe:0")
		packets.Stdout, packets.Stderr = &ticks, &discardWriter{}
		if err := packets.Run(); err != nil {
			if bounded.Err() != nil {
				return m, bounded.Err()
			}
			return m, ErrUnsupported
		}
		if ticks.finish() != nil || ticks.packets == 0 {
			return m, ErrUnsupported
		}
		duration = float64(ticks.total) * audioTimeBase
	}
	if m.Kind == "" || !finite(duration) || duration <= 0 || duration > 3600 || m.Width > 16384 || m.Height > 16384 {
		return Metadata{}, ErrUnsupported
	}
	m.DurationSeconds = duration
	m.Format = p.Format.Name
	if m.Kind == "video" {
		switch {
		case strings.Contains(p.Format.Name, "mov") || strings.Contains(p.Format.Name, "mp4"):
			m.MIMEType = "video/mp4"
			m.Format = "mp4"
		case strings.Contains(p.Format.Name, "webm") || strings.Contains(p.Format.Name, "matroska"):
			m.MIMEType = "video/webm"
			m.Format = "webm"
		default:
			return Metadata{}, ErrUnsupported
		}
	} else {
		switch {
		case p.Format.Name == "mp3":
			m.MIMEType = "audio/mpeg"
			m.Format = "mp3"
		case strings.Contains(p.Format.Name, "wav"):
			m.MIMEType = "audio/wav"
			m.Format = "wav"
		case strings.Contains(p.Format.Name, "mov") || strings.Contains(p.Format.Name, "mp4"):
			m.MIMEType = "audio/mp4"
			m.Format = "m4a"
		default:
			return Metadata{}, ErrUnsupported
		}
	}
	return m, nil
}

type probeContextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r probeContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

func mediaProbeReader(ctx context.Context, content io.ReadSeeker, size int64) (io.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := content.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.LimitReader(probeContextReader{ctx, content}, size), nil
}

func byteProbeCommand(ctx context.Context, content io.Reader, arguments ...string) *exec.Cmd {
	args := []string{"-v", "error", "-max_alloc", "67108864", "-protocol_whitelist", "pipe", "-enable_drefs", "0", "-use_absolute_path", "0", "-probesize", "8388608", "-analyzeduration", "5000000"}
	cmd := exec.CommandContext(ctx, "ffprobe", append(args, arguments...)...)
	cmd.Stdin = content
	return cmd
}

type packetDurationWriter struct {
	pending string
	total   int64
	packets int
}

func (w *packetDurationWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			if err := w.finish(); err != nil {
				return 0, err
			}
		} else {
			if len(w.pending) >= 128 {
				return 0, ErrUnsupported
			}
			w.pending += string(b)
		}
	}
	return len(p), nil
}

func (w *packetDurationWriter) finish() error {
	line := strings.TrimSpace(w.pending)
	w.pending = ""
	if line == "" {
		return nil
	}
	value, _, _ := strings.Cut(line, ",")
	ticks, err := strconv.ParseInt(value, 10, 64)
	if err != nil || ticks < 0 || ticks > math.MaxInt64-w.total || w.packets >= 1_000_000 {
		return ErrUnsupported
	}
	w.total += ticks
	w.packets++
	return nil
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func parseRatio(v string) float64 {
	a, b, ok := strings.Cut(v, "/")
	if !ok {
		n, _ := strconv.ParseFloat(v, 64)
		return n
	}
	n, _ := strconv.ParseFloat(a, 64)
	d, _ := strconv.ParseFloat(b, 64)
	if d <= 0 {
		return 0
	}
	return n / d
}

type cappedWriter struct{ bytes.Buffer }

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.Len()+len(p) > 64<<10 {
		return 0, ErrUnsupported
	}
	return w.Buffer.Write(p)
}

type discardWriter struct{}

func (*discardWriter) Write(p []byte) (int, error) { return len(p), nil }
