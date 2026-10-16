package lsm9ds1_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/vane"
	"github.com/JonasBorgesLM/vane/chip/lsm9ds1"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// transcripts holds the transcripts recorded on a Pi (RF-11). Each is the bus
// traffic of one scenario below on a real LSM9DS1, and replaying it holds the
// driver to the protocol it spoke then: identities first, CTRL_REG8 before any
// block read, the bare sub-address on 0x6A and the MSB on 0x1C.
const transcripts = "../../testdata/transcripts"

const expectPrefix = "# expect "

// scenarios run in both the recording and the replay. pause lets time pass on
// the chip: a sleep on the Pi, nothing in a replay, whose answers are recorded.
// The clock jumps past the turn-on samples, so Read is not refused for them.
// Changing a scenario invalidates its transcript: record it again on the Pi.
var scenarios = map[string]func(ctx context.Context, bus vane.Bus, pause func(time.Duration)) ([]string, error){
	"read": func(ctx context.Context, bus vane.Bus, pause func(time.Duration)) ([]string, error) {
		c := &clock{t: fixedTime}
		s, err := lsm9ds1.New(ctx, bus, lsm9ds1.WithClock(c.now))
		if err != nil {
			return nil, fmt.Errorf("New: %w", err)
		}
		pause(300 * time.Millisecond)
		c.t = fixedTime.Add(time.Hour)
		var got []string
		for i := range 2 {
			m, err := s.Read(ctx)
			if err != nil {
				return got, fmt.Errorf("Read %d: %w", i, err)
			}
			got = append(got, fmt.Sprintf("read %d: %s", i, motion(m)))
			v, err := s.ReadMagnetic(ctx)
			if err != nil {
				return got, fmt.Errorf("ReadMagnetic %d: %w", i, err)
			}
			got = append(got, fmt.Sprintf("magnetic %d: %s", i, axes(v)))
			pause(150 * time.Millisecond)
		}
		return got, s.Close(ctx)
	},
	"batch": func(ctx context.Context, bus vane.Bus, pause func(time.Duration)) ([]string, error) {
		c := &clock{t: fixedTime}
		s, err := lsm9ds1.New(ctx, bus, lsm9ds1.WithClock(c.now), lsm9ds1.WithFIFO())
		if err != nil {
			return nil, fmt.Errorf("New: %w", err)
		}
		dst := make([]lsm9ds1.Motion, 32)
		var got []string
		for i := range 3 {
			pause(100 * time.Millisecond)
			n, err := s.ReadBatch(ctx, dst)
			if err != nil {
				return got, fmt.Errorf("ReadBatch %d: %w", i, err)
			}
			got = append(got, fmt.Sprintf("batch %d: %d samples", i, n))
			for j, m := range dst[:n] {
				got = append(got, fmt.Sprintf("batch %d sample %d: %s", i, j, motion(m)))
			}
		}
		return got, s.Close(ctx)
	},
}

func axes[T ~float64](v lsm9ds1.Vector3[T]) string {
	return fmt.Sprintf("raw %d %d %d value %.5f %.5f %.5f flags [%v][%v][%v]", v.X.Raw, v.Y.Raw, v.Z.Raw, float64(v.X.Value), float64(v.Y.Value), float64(v.Z.Value), v.X.Flags, v.Y.Flags, v.Z.Flags)
}

func motion(m lsm9ds1.Motion) string {
	return "gyro " + axes(m.AngularRate) + " accel " + axes(m.Acceleration)
}

func TestRecordedTranscriptsReplay(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(transcripts, "lsm9ds1-*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no LSM9DS1 transcript under %s: the replay would pass by having nothing to replay", transcripts)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f) // #nosec G304 -- f comes from a glob over the constant testdata directory
			if err != nil {
				t.Fatal(err)
			}
			name := scenarioOf(data)
			run, ok := scenarios[name]
			if !ok {
				t.Fatalf("the transcript names scenario %q, which does not exist", name)
			}
			want := expectations(data)
			if len(want) == 0 {
				t.Fatal("the transcript holds no expectation lines")
			}
			got, err := run(bg, vanetest.NewReplayBus(t, bytes.NewReader(data)), func(time.Duration) {})
			if err != nil {
				t.Fatalf("scenario: %v", err)
			}
			if !slices.Equal(got, want) {
				t.Errorf("the driver reads the recorded bytes differently:\n got: %q\nwant: %q", got, want)
			}
		})
	}
}

func scenarioOf(transcript []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(transcript))
	for sc.Scan() {
		if name, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "# scenario "); ok {
			return name
		}
	}
	return ""
}

func expectations(transcript []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(transcript))
	for sc.Scan() {
		// TrimSpace: a checkout on Windows may end each line in \r\n.
		if line, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), expectPrefix); ok {
			out = append(out, line)
		}
	}
	return out
}
