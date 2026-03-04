// It reads the accelerometer via the Windows Sensor API — no separate sensor
// daemon required. Windows port of github.com/taigrr/spank.
package main

import (
"bytes"
"context"
"embed"
"fmt"
"io"
"math"
"math/rand"
"os"
"os/signal"
"path/filepath"
"sort"
"sync"
"time"

"github.com/charmbracelet/fang"
"github.com/gopxl/beep/v2"
"github.com/gopxl/beep/v2/mp3"
"github.com/gopxl/beep/v2/speaker"
"github.com/spf13/cobra"
"github.com/usivagna/Spank-windows/detector"
"github.com/usivagna/Spank-windows/sensor"
)

var version = "dev"

//go:embed audio/pain/*.mp3
var painAudio embed.FS

//go:embed audio/sexy/*.mp3
var sexyAudio embed.FS

//go:embed audio/halo/*.mp3
var haloAudio embed.FS

var (
sexyMode     bool
haloMode     bool
customPath   string
minAmplitude float64
cooldownMs   int
)

// sensorErr receives any error from the sensor worker.
var sensorErr = make(chan error, 1)

type playMode int

const (
modeRandom playMode = iota
modeEscalation
)

const (
// decayHalfLife is how many seconds of inactivity before intensity
// halves. Controls how fast escalation fades.
decayHalfLife = 30.0
// defaultCooldownMs is the default cooldown between audio responses.
defaultCooldownMs = 750
// sensorPollInterval is how often we check for new accelerometer data.
sensorPollInterval = 10 * time.Millisecond
// maxSampleBatch caps the number of accelerometer samples processed
// per tick to avoid falling behind.
maxSampleBatch = 200
// sensorStartupDelay gives the sensor time to start producing data.
sensorStartupDelay = 100 * time.Millisecond
)

type soundPack struct {
name   string
fs     embed.FS
dir    string
mode   playMode
files  []string
custom bool
}

func (sp *soundPack) loadFiles() error {
if sp.custom {
entries, err := os.ReadDir(sp.dir)
if err != nil {
return err
}
sp.files = make([]string, 0, len(entries))
for _, entry := range entries {
if !entry.IsDir() {
sp.files = append(sp.files, filepath.Join(sp.dir, entry.Name()))
}
}
} else {
entries, err := sp.fs.ReadDir(sp.dir)
if err != nil {
return err
}
sp.files = make([]string, 0, len(entries))
for _, entry := range entries {
if !entry.IsDir() {
sp.files = append(sp.files, sp.dir+"/"+entry.Name())
}
}
}
sort.Strings(sp.files)
if len(sp.files) == 0 {
return fmt.Errorf("no audio files found in %s", sp.dir)
}
return nil
}

type slapTracker struct {
mu       sync.Mutex
score    float64
lastTime time.Time
total    int
halfLife float64 // seconds
scale    float64 // controls the escalation curve shape
pack     *soundPack
}

// newSlapTracker creates a slapTracker.
// scale maps the exponential curve so that sustained max-rate
// slapping (one per cooldown) reaches the final file. At steady
// state the score converges to ssMax; we set scale so that score
// maps to the last index.
func newSlapTracker(pack *soundPack, cooldown time.Duration) *slapTracker {
cooldownSec := cooldown.Seconds()
ssMax := 1.0 / (1.0 - math.Pow(0.5, cooldownSec/decayHalfLife))
scale := (ssMax - 1) / math.Log(float64(len(pack.files)+1))
return &slapTracker{
halfLife: decayHalfLife,
scale:    scale,
pack:     pack,
}
}

func (st *slapTracker) record(now time.Time) (int, float64) {
st.mu.Lock()
defer st.mu.Unlock()
if !st.lastTime.IsZero() {
elapsed := now.Sub(st.lastTime).Seconds()
st.score *= math.Pow(0.5, elapsed/st.halfLife)
}
st.score += 1.0
st.lastTime = now
st.total++
return st.total, st.score
}

func (st *slapTracker) getFile(score float64) string {
if st.pack.mode == modeRandom {
return st.pack.files[rand.Intn(len(st.pack.files))]
}
// Escalation: 1-exp(-x) curve maps score to file index.
maxIdx := len(st.pack.files) - 1
idx := int(float64(len(st.pack.files)) * (1.0 - math.Exp(-(score-1)/st.scale)))
if idx > maxIdx {
idx = maxIdx
}
return st.pack.files[idx]
}

func main() {
cmd := &cobra.Command{
Use:   "spank",
Short: "Yells 'ow!' when you slap the laptop",
Long: `spank reads the Windows accelerometer via the Sensor API
and plays audio responses when a slap or hit is detected.
Works on Windows laptops with built-in accelerometers (Surface, Lenovo,
HP, Dell, and others with motion sensors).
Use --sexy for a different experience. In sexy mode, the more you slap
within a minute, the more intense the sounds become.
Use --halo to play random audio clips from Halo soundtracks on each slap.`,
Version: version,
RunE: func(cmd *cobra.Command, args []string) error {
return run(cmd.Context())
},
SilenceUsage: true,
}
cmd.Flags().BoolVarP(&sexyMode, "sexy", "s", false, "Enable sexy mode")
cmd.Flags().BoolVarP(&haloMode, "halo", "H", false, "Enable halo mode")
cmd.Flags().StringVarP(&customPath, "custom", "c", "", "Path to custom MP3 audio directory")
cmd.Flags().Float64Var(&minAmplitude, "min-amplitude", 0.3, "Minimum amplitude threshold (0.0-1.0, lower = more sensitive)")
cmd.Flags().IntVar(&cooldownMs, "cooldown", defaultCooldownMs, "Cooldown between responses in milliseconds")
if err := fang.Execute(context.Background(), cmd); err != nil {
os.Exit(1)
}
}

func run(ctx context.Context) error {
modeCount := 0
if sexyMode {
modeCount++
}
if haloMode {
modeCount++
}
if customPath != "" {
modeCount++
}
if modeCount > 1 {
return fmt.Errorf("--sexy, --halo, and --custom are mutually exclusive; pick one")
}
if minAmplitude < 0 || minAmplitude > 1 {
return fmt.Errorf("--min-amplitude must be between 0.0 and 1.0")
}
var pack *soundPack
switch {
case customPath != "":
pack = &soundPack{name: "custom", dir: customPath, mode: modeRandom, custom: true}
case sexyMode:
pack = &soundPack{name: "sexy", fs: sexyAudio, dir: "audio/sexy", mode: modeEscalation}
case haloMode:
pack = &soundPack{name: "halo", fs: haloAudio, dir: "audio/halo", mode: modeRandom}
default:
pack = &soundPack{name: "pain", fs: painAudio, dir: "audio/pain", mode: modeRandom}
}
if err := pack.loadFiles(); err != nil {
return fmt.Errorf("loading %s audio: %w", pack.name, err)
}
ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
defer cancel()
// Initialize the Windows accelerometer sensor.
reader := sensor.NewReader()
defer reader.Close()
if err := reader.Init(); err != nil {
return fmt.Errorf("accelerometer init: %w", err)
}
// Start the sensor polling in a background goroutine.
go func() {
if err := reader.Run(ctx); err != nil {
sensorErr <- err
}
}()
// Give the sensor a moment to start producing data.
time.Sleep(sensorStartupDelay)
cooldown := time.Duration(cooldownMs) * time.Millisecond
return listenForSlaps(ctx, pack, reader, cooldown)
}

func listenForSlaps(ctx context.Context, pack *soundPack, reader *sensor.Reader, cooldown time.Duration) error {
tracker := newSlapTracker(pack, cooldown)
speakerInit := false
det := detector.New()
var lastEventTime time.Time
var lastYell time.Time
fmt.Printf("spank: listening for slaps in %s mode... (ctrl+c to quit)\n", pack.name)
// Drain samples from the sensor reader channel.
samples := reader.Samples()
for {
select {
case <-ctx.Done():
fmt.Println("\nbye!")
return nil
case err := <-sensorErr:
return fmt.Errorf("sensor worker failed: %w", err)
case sample := <-samples:
now := time.Now()
tNow := float64(now.UnixNano()) / 1e9
det.Process(sample.X, sample.Y, sample.Z, tNow)
if len(det.Events) == 0 {
continue
}
ev := det.Events[len(det.Events)-1]
if ev.Time == lastEventTime {
continue
}
if time.Since(lastYell) <= cooldown {
continue
}
if ev.Amplitude < minAmplitude {
continue
}
lastYell = now
num, score := tracker.record(now)
file := tracker.getFile(score)
fmt.Printf("slap #%d [%s amp=%.5fg] -> %s\n", num, ev.Severity, ev.Amplitude, file)
go playAudio(pack, file, &speakerInit)
}
}
}

var speakerMu sync.Mutex

func playAudio(pack *soundPack, path string, speakerInit *bool) {
var streamer beep.StreamSeekCloser
var format beep.Format
if pack.custom {
file, err := os.Open(path)
if err != nil {
fmt.Fprintf(os.Stderr, "spank: open %s: %v\n", path, err)
return
}
defer file.Close()
var decErr error
streamer, format, decErr = mp3.Decode(file)
if decErr != nil {
fmt.Fprintf(os.Stderr, "spank: decode %s: %v\n", path, decErr)
return
}
} else {
data, err := pack.fs.ReadFile(path)
if err != nil {
fmt.Fprintf(os.Stderr, "spank: read %s: %v\n", path, err)
return
}
var decErr error
streamer, format, decErr = mp3.Decode(io.NopCloser(bytes.NewReader(data)))
if decErr != nil {
fmt.Fprintf(os.Stderr, "spank: decode %s: %v\n", path, decErr)
return
}
}
defer streamer.Close()
speakerMu.Lock()
if !*speakerInit {
speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
*speakerInit = true
}
speakerMu.Unlock()
done := make(chan bool)
speaker.Play(beep.Seq(streamer, beep.Callback(func() {
done <- true
})))
<-done
}
