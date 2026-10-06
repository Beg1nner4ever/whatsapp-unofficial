package wa

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
)

// analyzeOggOpus extracts the duration of an Ogg Opus file and produces a
// placeholder waveform, which WhatsApp shows on voice messages.
func analyzeOggOpus(data []byte) (uint32, []byte, error) {
	if len(data) < 4 || string(data[:4]) != "OggS" {
		return 0, nil, errors.New("not a valid Ogg file")
	}

	// Opus granule positions always count 48 kHz samples; the rate in
	// OpusHead is only the original input rate.
	const granuleRate = 48000
	var lastGranule uint64
	var preSkip uint16
	foundHead := false

	for i := 0; i < len(data); {
		if i+27 >= len(data) {
			break
		}
		if string(data[i:i+4]) != "OggS" {
			i++
			continue
		}
		granule := binary.LittleEndian.Uint64(data[i+6 : i+14])
		pageSeq := binary.LittleEndian.Uint32(data[i+18 : i+22])
		segments := int(data[i+26])
		if i+27+segments >= len(data) {
			break
		}
		pageSize := 27 + segments
		for _, l := range data[i+27 : i+27+segments] {
			pageSize += int(l)
		}
		end := i + pageSize
		if end > len(data) {
			end = len(data)
		}
		if !foundHead && pageSeq <= 1 {
			page := data[i:end]
			if pos := bytes.Index(page, []byte("OpusHead")); pos >= 0 && pos+8+8 <= len(page) {
				head := page[pos+8:]
				preSkip = binary.LittleEndian.Uint16(head[2:4])
				foundHead = true
			}
		}
		if granule != 0 {
			lastGranule = granule
		}
		i += pageSize
	}

	var duration uint32
	if lastGranule > uint64(preSkip) {
		duration = uint32(math.Ceil(float64(lastGranule-uint64(preSkip)) / granuleRate))
	} else {
		duration = uint32(len(data) / 2000) // rough fallback
	}
	if duration < 1 {
		duration = 1
	} else if duration > 300 {
		duration = 300
	}
	return duration, placeholderWaveform(duration), nil
}

// placeholderWaveform generates a natural-looking 64-sample waveform.
func placeholderWaveform(duration uint32) []byte {
	const n = 64
	rng := rand.New(rand.NewSource(int64(duration)))
	freq := float64(min(int(duration), 120)) / 30.0
	out := make([]byte, n)
	for i := range out {
		pos := float64(i) / n
		v := 35*math.Sin(pos*math.Pi*freq*8) + 17.5*math.Sin(pos*math.Pi*freq*16)
		v += (rng.Float64() - 0.5) * 15
		v = v*(0.7+0.3*math.Sin(pos*math.Pi)) + 50
		out[i] = byte(math.Max(0, math.Min(100, v)))
	}
	return out
}
