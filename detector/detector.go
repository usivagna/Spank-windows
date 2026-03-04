// Package detector implements vibration detection from accelerometer data.
// Ported from github.com/taigrr/apple-silicon-accelerometer/detector.
package detector

import (
	"math"
	"time"
)

// SampleRate is the expected input sample rate in Hz.
const SampleRate = 100

// Event represents a detected vibration event.
type Event struct {
	Time      time.Time
	Severity  string
	Symbol    string
	Label     string
	Amplitude float64
	Sources   []string
}

// Detector processes accelerometer data to detect vibrations.
type Detector struct {
	SampleCount int
	FS          int

	// Latest raw values
	LatestRaw [3]float64
	LatestMag float64

	// High-pass filter for gravity removal
	hpAlpha   float64
	hpPrevRaw [3]float64
	hpPrevOut [3]float64
	hpReady   bool

	// Waveform history
	Waveform    *RingFloat
	WaveformXYZ *RingVec3

	// STA/LTA detector (3 timescales)
	sta          [3]float64
	lta          [3]float64
	staN         [3]int
	ltaN         [3]int
	staLTAOn     [3]float64
	staLTAOff    [3]float64
	STALTAActive [3]bool
	STALTALatest [3]float64
	STALTARings  [3]*RingFloat

	// CUSUM
	cusumPos float64
	cusumNeg float64
	cusumMu  float64
	cusumK   float64
	cusumH   float64
	CUSUMVal float64

	// Kurtosis
	kurtBuf  *RingFloat
	Kurtosis float64

	// Peak / MAD / crest factor
	peakBuf  *RingFloat
	Crest    float64
	RMS      float64
	Peak     float64
	MADSigma float64

	// RMS trend
	RMSTrend  *RingFloat
	rmsWindow *RingFloat

	// Events
	Events   []Event
	lastEvtT float64

	// Internal counters
	staDec  int
	kurtDec int
	rmsDec  int
}

// New creates a new Detector with default parameters.
func New() *Detector {
	fs := SampleRate
	n5 := fs * 5

	d := &Detector{
		FS:      fs,
		hpAlpha: 0.95,

		Waveform:    NewRingFloat(n5),
		WaveformXYZ: NewRingVec3(n5),

		staN:      [3]int{3, 15, 50},
		ltaN:      [3]int{100, 500, 2000},
		staLTAOn:  [3]float64{3.0, 2.5, 2.0},
		staLTAOff: [3]float64{1.5, 1.3, 1.2},

		cusumK: 0.0005,
		cusumH: 0.01,

		kurtBuf:  NewRingFloat(100),
		Kurtosis: 3.0,

		peakBuf: NewRingFloat(200),
		Crest:   1.0,

		RMSTrend:  NewRingFloat(100),
		rmsWindow: NewRingFloat(fs),
	}

	for i := range 3 {
		d.lta[i] = 1e-10
		d.STALTALatest[i] = 1.0
		d.STALTARings[i] = NewRingFloat(30)
	}

	return d
}

// Process ingests one accelerometer sample and returns the dynamic magnitude.
func (d *Detector) Process(ax, ay, az, tNow float64) float64 {
	d.SampleCount++
	d.LatestRaw = [3]float64{ax, ay, az}
	d.LatestMag = math.Sqrt(ax*ax + ay*ay + az*az)

	if !d.hpReady {
		d.hpPrevRaw = [3]float64{ax, ay, az}
		d.hpReady = true
		d.Waveform.Push(0)
		return 0
	}

	// High-pass filter to remove gravity
	a := d.hpAlpha
	hx := a * (d.hpPrevOut[0] + ax - d.hpPrevRaw[0])
	hy := a * (d.hpPrevOut[1] + ay - d.hpPrevRaw[1])
	hz := a * (d.hpPrevOut[2] + az - d.hpPrevRaw[2])
	d.hpPrevRaw = [3]float64{ax, ay, az}
	d.hpPrevOut = [3]float64{hx, hy, hz}
	mag := math.Sqrt(hx*hx + hy*hy + hz*hz)

	d.Waveform.Push(mag)
	d.WaveformXYZ.Push3(hx, hy, hz)

	// RMS trend
	d.rmsWindow.Push(mag)
	d.rmsDec++
	if d.rmsDec >= max(1, d.FS/10) {
		d.rmsDec = 0
		vals := d.rmsWindow.Slice()
		if len(vals) > 0 {
			var s float64
			for _, v := range vals {
				s += v * v
			}
			d.RMSTrend.Push(math.Sqrt(s / float64(len(vals))))
		}
	}

	var evts []detection

	// STA/LTA
	e := mag * mag
	for i := range 3 {
		d.sta[i] += (e - d.sta[i]) / float64(d.staN[i])
		d.lta[i] += (e - d.lta[i]) / float64(d.ltaN[i])
		ratio := d.sta[i] / (d.lta[i] + 1e-30)
		d.STALTALatest[i] = ratio
		was := d.STALTAActive[i]
		if ratio > d.staLTAOn[i] && !was {
			d.STALTAActive[i] = true
			evts = append(evts, detection{source: "STA/LTA"})
		} else if ratio < d.staLTAOff[i] {
			d.STALTAActive[i] = false
		}
	}
	d.staDec++
	if d.staDec >= max(1, d.FS/30) {
		d.staDec = 0
		for i := range 3 {
			d.STALTARings[i].Push(d.STALTALatest[i])
		}
	}

	// CUSUM
	d.cusumMu += 0.0001 * (mag - d.cusumMu)
	d.cusumPos = math.Max(0, d.cusumPos+mag-d.cusumMu-d.cusumK)
	d.cusumNeg = math.Max(0, d.cusumNeg-mag+d.cusumMu-d.cusumK)
	d.CUSUMVal = math.Max(d.cusumPos, d.cusumNeg)
	if d.cusumPos > d.cusumH {
		evts = append(evts, detection{source: "CUSUM"})
		d.cusumPos = 0
	}
	if d.cusumNeg > d.cusumH {
		evts = append(evts, detection{source: "CUSUM"})
		d.cusumNeg = 0
	}

	// Kurtosis
	d.kurtBuf.Push(mag)
	d.kurtDec++
	if d.kurtDec >= 10 && d.kurtBuf.Len() >= 50 {
		d.kurtDec = 0
		buf := d.kurtBuf.Slice()
		n := float64(len(buf))
		mu := sum(buf) / n
		var m2, m4 float64
		for _, v := range buf {
			diff := v - mu
			d2 := diff * diff
			m2 += d2
			m4 += d2 * d2
		}
		m2 /= n
		m4 /= n
		d.Kurtosis = m4 / (m2*m2 + 1e-30)
		if d.Kurtosis > 6 {
			evts = append(evts, detection{source: "KURTOSIS"})
		}
	}

	// Peak / MAD
	d.peakBuf.Push(mag)
	if d.peakBuf.Len() >= 50 && d.SampleCount%10 == 0 {
		buf := d.peakBuf.Slice()
		sorted := sortedCopy(buf)
		n := len(sorted)
		median := sorted[n/2]

		devs := make([]float64, n)
		for i, v := range sorted {
			devs[i] = math.Abs(v - median)
		}
		sortFloat64s(devs)
		mad := devs[n/2]
		sigma := 1.4826*mad + 1e-30
		d.MADSigma = sigma

		var s float64
		var pk float64
		for _, v := range buf {
			s += v * v
			if math.Abs(v) > pk {
				pk = math.Abs(v)
			}
		}
		d.RMS = math.Sqrt(s / float64(n))
		d.Peak = pk
		d.Crest = pk / (d.RMS + 1e-30)

		dev := math.Abs(mag-median) / sigma
		if dev > 2.0 {
			evts = append(evts, detection{source: "PEAK"})
		}
	}

	if len(evts) > 0 && (tNow-d.lastEvtT) > 0.01 {
		d.lastEvtT = tNow
		d.classify(evts, tNow, mag)
	}

	return mag
}

type detection struct {
	source string
}

func (d *Detector) classify(dets []detection, t, amp float64) {
	sources := make(map[string]bool)
	for _, det := range dets {
		sources[det.source] = true
	}
	ns := len(sources)

	var sev, sym, lbl string
	switch {
	case ns >= 4 && amp > 0.05:
		sev, sym, lbl = "CHOC_MAJEUR", "★", "MAJOR"
	case ns >= 3 && amp > 0.02:
		sev, sym, lbl = "CHOC_MOYEN", "▲", "shock"
	case sources["PEAK"] && amp > 0.005:
		sev, sym, lbl = "MICRO_CHOC", "△", "micro-choc"
	case (sources["STA/LTA"] || sources["CUSUM"]) && amp > 0.003:
		sev, sym, lbl = "VIBRATION", "●", "vibration"
	case amp > 0.001:
		sev, sym, lbl = "VIB_LEGERE", "○", "light-vib"
	default:
		sev, sym, lbl = "MICRO_VIB", "·", "micro-vib"
	}

	srcList := make([]string, 0, len(sources))
	for s := range sources {
		srcList = append(srcList, s)
	}

	ev := Event{
		Time:      time.Unix(int64(t), int64((t-math.Floor(t))*1e9)),
		Severity:  sev,
		Symbol:    sym,
		Label:     lbl,
		Amplitude: amp,
		Sources:   srcList,
	}

	d.Events = append(d.Events, ev)
	// Keep max 500 events
	if len(d.Events) > 500 {
		d.Events = d.Events[len(d.Events)-500:]
	}
}

// STALTAOn returns the STA/LTA on-threshold for the given timescale index.
func (d *Detector) STALTAOn(i int) float64 {
	return d.staLTAOn[i]
}

// Helpers

func sum(s []float64) float64 {
	var t float64
	for _, v := range s {
		t += v
	}
	return t
}

func sortedCopy(s []float64) []float64 {
	c := make([]float64, len(s))
	copy(c, s)
	sortFloat64s(c)
	return c
}

func sortFloat64s(s []float64) {
	// Simple insertion sort — small slices only (<=200)
	for i := 1; i < len(s); i++ {
		key := s[i]
		j := i - 1
		for j >= 0 && s[j] > key {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = key
	}
}
