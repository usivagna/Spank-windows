//go:build windows

// Windows Sensor API accelerometer reader using COM ISensorManager.
//
// This uses the Windows 7+ Sensor API to access the built-in accelerometer
// found in many laptops (Surface, Lenovo, HP, Dell, etc.).
//
// The COM interfaces used:
//   - ISensorManager (CLSID_SensorManager)
//   - ISensor (SENSOR_TYPE_ACCELEROMETER_3D)
//   - ISensorDataReport

package sensor

import (
"context"
"fmt"
"math"
"sync"
"syscall"
"time"
"unsafe"

"golang.org/x/sys/windows"
)

// GUIDs for the Windows Sensor API COM interfaces.
var (
// CLSID_SensorManager = {77A1C827-FCD2-4689-8915-9D613CC5FA3E}
clsidSensorManager = windows.GUID{
Data1: 0x77A1C827,
Data2: 0xFCD2,
Data3: 0x4689,
Data4: [8]byte{0x89, 0x15, 0x9D, 0x61, 0x3C, 0xC5, 0xFA, 0x3E},
}
// IID_ISensorManager = {BD77DB67-45A8-42DC-8D00-6DCF15F8377A}
iidSensorManager = windows.GUID{
Data1: 0xBD77DB67,
Data2: 0x45A8,
Data3: 0x42DC,
Data4: [8]byte{0x8D, 0x00, 0x6D, 0xCF, 0x15, 0xF8, 0x37, 0x7A},
}
// SENSOR_TYPE_ACCELEROMETER_3D = {C2FB0F5F-E2D2-4C78-BCD0-352A9582819D}
sensorTypeAccelerometer3D = windows.GUID{
Data1: 0xC2FB0F5F,
Data2: 0xE2D2,
Data3: 0x4C78,
Data4: [8]byte{0xBC, 0xD0, 0x35, 0x2A, 0x95, 0x82, 0x81, 0x9D},
}
// SENSOR_DATA_TYPE_ACCELERATION_X_G = {3F8A69A2-07C5-4E48-A965-CD797AAB56D5} pid 2
sensorDataTypePropertySet = windows.GUID{
Data1: 0x3F8A69A2,
Data2: 0x07C5,
Data3: 0x4E48,
Data4: [8]byte{0xA9, 0x65, 0xCD, 0x79, 0x7A, 0xAB, 0x56, 0xD5},
}
pidAccelX = uint32(2)
pidAccelY = uint32(3)
pidAccelZ = uint32(4)
)

// PROPERTYKEY for sensor data fields.
type propertyKey struct {
Fmtid windows.GUID
Pid   uint32
}

// PROPVARIANT simplified — we only need VT_R8 (double).
type propVariant struct {
Vt       uint16
_        [6]byte
Val      float64 // union; for VT_R8 this is the double value
_padding [8]byte // ensure full 24-byte PROPVARIANT size
}

const (
vtR8      = 5  // VT_R8
vtR4      = 4  // VT_R4
coinitMTA = 0x0
)

var (
ole32               = windows.NewLazySystemDLL("ole32.dll")
procCoInit          = ole32.NewProc("CoInitializeEx")
procCoCreate        = ole32.NewProc("CoCreateInstance")
procCoUninit        = ole32.NewProc("CoUninitialize")
procProgIDFromCLSID = ole32.NewProc("ProgIDFromCLSID")
)

// ISensorManager vtable indices.
const (
smVtGetSensorsByType = 3 // after QueryInterface, AddRef, Release
)

// ISensorCollection vtable indices.
const (
scVtGetCount = 3
scVtGetAt    = 4
)

// ISensor vtable indices.
const (
sVtGetState = 5
sVtGetData  = 10
)

// ISensorDataReport vtable indices.
const (
drVtGetSensorValue = 4
)

// Reader reads accelerometer data from the Windows Sensor API.
type Reader struct {
mu          sync.Mutex
sensor      uintptr // *ISensor COM pointer
initialized bool
samples     chan Sample
}

// NewReader creates a new Windows accelerometer reader.
func NewReader() *Reader {
return &Reader{
samples: make(chan Sample, 1000),
}
}

// Samples returns the channel on which accelerometer samples are delivered.
func (r *Reader) Samples() <-chan Sample {
return r.samples
}

// comCall invokes a COM method via vtable.
func comCall(obj uintptr, vtableIdx int, args ...uintptr) (uintptr, error) {
vtable := *(*uintptr)(unsafe.Pointer(obj))
method := *(*uintptr)(unsafe.Pointer(vtable + uintptr(vtableIdx)*unsafe.Sizeof(uintptr(0))))
allArgs := make([]uintptr, 0, 1+len(args))
allArgs = append(allArgs, obj) // this pointer
allArgs = append(allArgs, args...)
ret, _, err := syscall.SyscallN(method, allArgs...)
// COM methods return HRESULT; 0 = S_OK
if int32(ret) < 0 {
return ret, fmt.Errorf("COM call failed: HRESULT 0x%08X (%v)", uint32(ret), err)
}
return ret, nil
}

// comRelease calls IUnknown::Release on a COM object.
func comRelease(obj uintptr) {
if obj != 0 {
comCall(obj, 2) // Release is vtable index 2
}
}

// Init initializes COM and finds the accelerometer sensor.
func (r *Reader) Init() error {
r.mu.Lock()
defer r.mu.Unlock()
if r.initialized {
return nil
}
// Initialize COM in MTA mode.
ret, _, _ := procCoInit.Call(0, coinitMTA)
if int32(ret) < 0 && int32(ret) != -2147417850 { // RPC_E_CHANGED_MODE is ok
return fmt.Errorf("CoInitializeEx failed: 0x%08X", uint32(ret))
}
// Create ISensorManager instance.
var sensorManager uintptr
ret, _, _ = procCoCreate.Call(
uintptr(unsafe.Pointer(&clsidSensorManager)),
0,   // pUnkOuter
1|4, // CLSCTX_INPROC_SERVER | CLSCTX_LOCAL_SERVER
uintptr(unsafe.Pointer(&iidSensorManager)),
uintptr(unsafe.Pointer(&sensorManager)),
)
if int32(ret) < 0 {
return fmt.Errorf("failed to create SensorManager: 0x%08X (is the Windows Sensor service running?)", uint32(ret))
}
defer comRelease(sensorManager)
// Get accelerometer sensors.
var sensorCollection uintptr
_, err := comCall(sensorManager, smVtGetSensorsByType,
uintptr(unsafe.Pointer(&sensorTypeAccelerometer3D)),
uintptr(unsafe.Pointer(&sensorCollection)),
)
if err != nil {
return fmt.Errorf("no accelerometer found: %w (does this device have an accelerometer?)", err)
}
defer comRelease(sensorCollection)
// Get count.
var count uint32
_, err = comCall(sensorCollection, scVtGetCount, uintptr(unsafe.Pointer(&count)))
if err != nil || count == 0 {
return fmt.Errorf("no accelerometer sensors available (count=%d)", count)
}
// Get the first sensor.
var sensorPtr uintptr
_, err = comCall(sensorCollection, scVtGetAt, 0, uintptr(unsafe.Pointer(&sensorPtr)))
if err != nil {
return fmt.Errorf("failed to get accelerometer sensor: %w", err)
}
r.sensor = sensorPtr
r.initialized = true
return nil
}

// readOneSample polls the sensor for one data report and extracts X,Y,Z in g.
func (r *Reader) readOneSample() (Sample, error) {
if r.sensor == 0 {
return Sample{}, fmt.Errorf("sensor not initialized")
}
// ISensor::GetData
var report uintptr
_, err := comCall(r.sensor, sVtGetData, uintptr(unsafe.Pointer(&report)))
if err != nil {
return Sample{}, fmt.Errorf("GetData failed: %w", err)
}
if report == 0 {
return Sample{}, fmt.Errorf("no data report available")
}
defer comRelease(report)
// Read X, Y, Z acceleration values.
x, err := r.readSensorValue(report, pidAccelX)
if err != nil {
return Sample{}, fmt.Errorf("reading X: %w", err)
}
y, err := r.readSensorValue(report, pidAccelY)
if err != nil {
return Sample{}, fmt.Errorf("reading Y: %w", err)
}
z, err := r.readSensorValue(report, pidAccelZ)
if err != nil {
return Sample{}, fmt.Errorf("reading Z: %w", err)
}
return Sample{
X:         x,
Y:         y,
Z:         z,
Timestamp: time.Now(),
}, nil
}

// readSensorValue reads a float64 value from a data report.
func (r *Reader) readSensorValue(report uintptr, pid uint32) (float64, error) {
key := propertyKey{
Fmtid: sensorDataTypePropertySet,
Pid:   pid,
}
var pv propVariant
_, err := comCall(report, drVtGetSensorValue,
uintptr(unsafe.Pointer(&key)),
uintptr(unsafe.Pointer(&pv)),
)
if err != nil {
return 0, err
}
switch pv.Vt {
case vtR8:
return pv.Val, nil
case vtR4:
// VT_R4 is a 32-bit float stored in the first 4 bytes of the union
bits := *(*uint32)(unsafe.Pointer(&pv.Val))
return float64(math.Float32frombits(bits)), nil
default:
return 0, fmt.Errorf("unexpected PROPVARIANT type: %d", pv.Vt)
}
}

// Run starts polling the accelerometer and sending samples to the channel.
// It blocks until the context is cancelled.
func (r *Reader) Run(ctx context.Context) error {
if err := r.Init(); err != nil {
return err
}
ticker := time.NewTicker(10 * time.Millisecond) // 100 Hz
defer ticker.Stop()
for {
select {
case <-ctx.Done():
return nil
case <-ticker.C:
sample, err := r.readOneSample()
if err != nil {
// Skip failed reads; sensor may not have new data yet.
continue
}
select {
case r.samples <- sample:
default:
// Drop sample if channel is full.
}
}
}
}

// Close releases the COM sensor object.
func (r *Reader) Close() {
r.mu.Lock()
defer r.mu.Unlock()
if r.sensor != 0 {
comRelease(r.sensor)
r.sensor = 0
}
r.initialized = false
}
