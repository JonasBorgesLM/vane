package hts221_test

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
	"github.com/JonasBorgesLM/vane/chip/hts221"
	"github.com/JonasBorgesLM/vane/vanetest"
)

// transcripts holds the transcripts recorded on a Pi (RF-11). Each one is the
// bus traffic of transcriptScenario on a real chip; replaying it here proves the
// driver still speaks to the chip exactly as it did then, on any machine.
const transcripts = "../../testdata/transcripts"

// expectPrefix marks the comment lines in which a recording keeps what the
// driver returned, so a replay checks the conversion as well as the protocol.
const expectPrefix = "# expect "

const transcriptReads = 3

// transcriptScenario is what both the recording and the replay run. Changing it
// invalidates every recorded transcript: record them again on the Pi.
func transcriptScenario(ctx context.Context, bus vane.Bus) ([]string, error) {
	s, err := hts221.New(ctx, bus, hts221.WithClock(func() time.Time { return time.Time{} }))
	if err != nil {
		return nil, fmt.Errorf("New: %w", err)
	}
	var got []string
	for i := range transcriptReads {
		smp, err := s.Read(ctx)
		if err != nil {
			return got, fmt.Errorf("read %d: %w", i, err)
		}
		got = append(got, fmt.Sprintf("read %d: humidity %.4f raw %d flags [%v], temperature %.4f raw %d flags [%v]",
			i, smp.Humidity.Value, smp.Humidity.Raw, smp.Humidity.Flags,
			smp.Temperature.Value, smp.Temperature.Raw, smp.Temperature.Flags))
	}
	if err := s.Close(ctx); err != nil {
		return got, fmt.Errorf("Close: %w", err)
	}
	return got, nil
}

func TestRecordedTranscriptsReplay(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(transcripts, "hts221-*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no HTS221 transcript under %s: the replay would pass by having nothing to replay", transcripts)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f) // #nosec G304 -- f comes from a glob over the constant testdata directory
			if err != nil {
				t.Fatal(err)
			}
			want := expectations(data)
			if len(want) != transcriptReads {
				t.Fatalf("the transcript holds %d expectation lines, want %d", len(want), transcriptReads)
			}
			bus := vanetest.NewReplayBus(t, bytes.NewReader(data))
			got, err := transcriptScenario(bg, bus)
			if err != nil {
				t.Fatalf("scenario: %v", err)
			}
			if !slices.Equal(got, want) {
				t.Errorf("the driver converts the recorded bytes differently:\n got: %q\nwant: %q", got, want)
			}
		})
	}
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
