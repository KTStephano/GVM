package gvm

import (
	"bufio"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type StatusCode = uint32

const (
	StatusDeviceNotFound StatusCode = 0x00
	StatusDeviceReady    StatusCode = 0x01
	StatusDeviceBusy     StatusCode = 0x02
)

// Only safe with 1 sender, but many receivers are allowed
type nonBlockingChan[T any] struct {
	channel  chan T
	count    atomic.Int32
	capacity int32
}

type Request struct {
	// This should either be 0 (no ID) or a unique number that represents the
	// send-receive interaction so that the software knows what this is in response to
	ID InteractionID
	// Differs from device to device, but specifies what action it should perform
	// with the given data
	Command uint32
	Data    []byte
}

type InteractionID = uint32

type Response struct {
	// This is the interrupt handler address associated with this device
	interruptAddr uint32
	// This should either be 0 (no ID) or a unique number that represents the
	// send-receive interaction so that the software knows what this is in response to
	id   InteractionID
	data []byte
	// If not nil then it signals to the CPU that something went wrong
	deviceErr error
}

type HardwareDeviceInfo struct {
	HWID uint32
	// For now unused, but could be extended in the future towards something like
	// https://wiki.osdev.org/AML
	Metadata []byte
}

type HardwareDevice interface {
	GetInfo() HardwareDeviceInfo

	// arguments: id, command, data
	TrySend(InteractionID, uint32, []byte) StatusCode

	// When a power cycle on -> off -> on comes in, this lets the device know to clear previous
	// state and restart
	Reset()

	// When a power down or poweroff request comes in, this lets the device know to end
	Close()
}

// Data that all hardware devices will need
type DeviceBaseInfo struct {
	// Specifies the entry into the interrupt table to match up with
	InterruptAddr uint32
	ResponseBus   *deviceResponseBus
}

func newNonBlockingChan[T any](capacity int32) *nonBlockingChan[T] {
	return &nonBlockingChan[T]{
		channel:  make(chan T, capacity),
		capacity: capacity,
	}
}

func (nc *nonBlockingChan[T]) send(data T) bool {
	newCount := nc.count.Add(1)
	if newCount > nc.capacity {
		nc.count.Add(-1)
		return false
	}

	nc.channel <- data
	return true
}

func (nc *nonBlockingChan[T]) receive() (T, bool) {
	v, ok := <-nc.channel
	if ok {
		nc.count.Add(-1)
	}

	return v, ok
}

func (nc *nonBlockingChan[T]) close() {
	nc.count.Store(nc.capacity + 1)
	close(nc.channel)
}

// deviceIndex can usually be 0 unless trying to multiplex one port to multiple devices
func NewResponse(interruptAddr uint32, id InteractionID, data []byte, err error) *Response {
	return &Response{
		interruptAddr: interruptAddr,
		id:            id,
		data:          data,
		deviceErr:     err,
	}
}

// ------- Begin no device marker
type nodevice struct {
	DeviceBaseInfo
}

func newNoDevice() HardwareDevice {
	return &nodevice{}
}

func (*nodevice) GetInfo() HardwareDeviceInfo {
	return HardwareDeviceInfo{}
}

func (*nodevice) TrySend(InteractionID, uint32, []byte) StatusCode {
	return StatusDeviceNotFound
}

func (*nodevice) Reset() {}

func (*nodevice) Close() {}

// ------- Begin system timer
type systemTimerData struct {
	duration time.Duration
	iid      InteractionID
}

type systemTimer struct {
	DeviceBaseInfo

	timerChan chan systemTimerData
	closed    atomic.Bool
}

func newSystemTimer(base DeviceBaseInfo) HardwareDevice {
	st := &systemTimer{
		DeviceBaseInfo: base,
		timerChan:      make(chan systemTimerData, 1),
	}

	// Start the timer goroutine
	go func() {
		t := time.NewTimer(time.Duration(math.MaxInt64))
		var iid InteractionID
		for {
			if st.closed.Load() {
				// Timer system shut down
				return
			}

			select {
			case <-t.C:
				// Use nil data in response since calling code will interpret our response
				// to mean the timer expired
				st.ResponseBus.Send(NewResponse(st.InterruptAddr, iid, nil, nil))
			case newTimer := <-st.timerChan:
				// New timer received - overwrite existing
				t = time.NewTimer(newTimer.duration)
				iid = newTimer.iid
			}
		}
	}()

	return st
}

func (t *systemTimer) GetInfo() HardwareDeviceInfo {
	return HardwareDeviceInfo{
		HWID: 0x01,
	}
}

func (t *systemTimer) TrySend(id InteractionID, command uint32, data []byte) StatusCode {
	if command == 1 {
		return StatusDeviceReady
	}

	t.timerChan <- systemTimerData{
		duration: time.Duration(uint32FromBytes(data)) * time.Microsecond,
		iid:      id,
	}

	return StatusDeviceReady
}

func (t *systemTimer) Reset() {
	// Send a new max timer to override the existing one
	t.timerChan <- systemTimerData{
		duration: time.Duration(math.MaxInt64),
		iid:      0,
	}
}

func (t *systemTimer) Close() {
	t.closed.Store(true)
	t.Reset()
}

// ------- Begin power controller
type powerController struct {
	DeviceBaseInfo
	vm *VM
}

func newPowerController(base DeviceBaseInfo, vm *VM) HardwareDevice {
	return &powerController{DeviceBaseInfo: base, vm: vm}
}

func (p *powerController) GetInfo() HardwareDeviceInfo {
	return HardwareDeviceInfo{
		HWID: 0x02,
	}
}

// Command of 1 -> get status
// Command of 2 -> perform restart
// Command of 3 -> perform poweroff
func (p *powerController) TrySend(_ InteractionID, command uint32, _ []byte) StatusCode {
	if command == 2 {
		p.vm.setInitialVMState()

		for _, device := range p.vm.devices {
			device.Reset()
		}
	} else if command == 3 {
		for _, device := range p.vm.devices {
			device.Close()
		}

		p.vm.errcode = errSystemShutdown
	}

	return StatusDeviceReady
}

func (*powerController) Reset() {}

func (*powerController) Close() {}

// ------- Begin memory management unit
type memoryManagement struct {
	DeviceBaseInfo
	vm *VM
	// These specify the min/max addresses for the heap when in
	// a privilege mode other than 0 (highest)
	minHeapAddr uint32
	maxHeapAddr uint32
}

func newMemoryManagement(base DeviceBaseInfo, vm *VM) HardwareDevice {
	return &memoryManagement{
		DeviceBaseInfo: base,
		vm:             vm,
		minHeapAddr:    0,
		maxHeapAddr:    uint32(len(vm.memory)),
	}
}

func (m *memoryManagement) GetInfo() HardwareDeviceInfo {
	return HardwareDeviceInfo{
		HWID: 0x03,
	}
}

func (m *memoryManagement) updateBounds() {
	if *m.vm.mode == 0 {
		m.vm.activeSegment = m.vm.memory[:]
		m.vm.stackOffsetBytes = 0
	} else {
		m.vm.activeSegment = m.vm.memory[m.minHeapAddr:m.maxHeapAddr]
		m.vm.stackOffsetBytes = m.minHeapAddr
	}
}

// Command of 1 -> get status
// Command of 2 -> set new min/max heap addr bounds for non-privileged mode
// Command of 3 -> update min/max heap addr based on privilege level
func (m *memoryManagement) TrySend(id InteractionID, command uint32, data []byte) StatusCode {
	if command == 1 {
		return StatusDeviceReady
	} else if command == 2 {
		m.minHeapAddr, m.maxHeapAddr = uint32FromBytes(data), uint32FromBytes(data[varchBytes:])
	}

	m.updateBounds()
	return StatusDeviceReady
}

func (m *memoryManagement) Reset() {
	m.minHeapAddr, m.maxHeapAddr = 0, uint32(len(m.vm.memory))
	m.updateBounds()
}

func (m *memoryManagement) Close() {}

// ------- Begin console IO manager
type consoleIO struct {
	sync.Mutex

	DeviceBaseInfo

	vm           *VM
	stdin        *bufio.Reader
	charRequests *nonBlockingChan[InteractionID]

	reset  bool
	closed bool
}

func newConsoleIO(base DeviceBaseInfo, vm *VM) HardwareDevice {
	io := &consoleIO{
		DeviceBaseInfo: base,
		vm:             vm,
		stdin:          bufio.NewReader(os.Stdin),
		charRequests:   newNonBlockingChan[InteractionID](32),
	}

	isResetOrClosed := func(lock bool) bool {
		if lock {
			io.Lock()
			defer io.Unlock()
		}

		r := io.reset
		// Clear the reset flag
		io.reset = false
		return r || io.closed
	}

	processOneRequest := func(iid InteractionID) {
		if isResetOrClosed(true) {
			return
		}

		// This should be the only routine that accesses stdin in the whole codebase
		r, _, _ := io.stdin.ReadRune()

		// Acquire the lock and then check again to see if we need to close
		io.Lock()
		defer io.Unlock()
		if isResetOrClosed(false) {
			io.stdin.UnreadRune()
			return
		}

		// Pack the rune into bytes and send a response over the CPU bus
		data := [4]byte{}
		uint32ToBytes(uint32(r), data[:])
		io.ResponseBus.Send(NewResponse(io.InterruptAddr, iid, data[:], nil))
	}

	// Start up the reader function
	go func() {
		for {
			iid, ok := io.charRequests.receive()
			if !ok {
				// IO device shut down
				return
			}

			processOneRequest(iid)
		}
	}()

	return io
}

func (*consoleIO) GetInfo() HardwareDeviceInfo {
	return HardwareDeviceInfo{
		HWID: 0x04,
	}
}

// Command of 1 -> get status
// Command of 2 -> write 32-bit character
// Command of 3 -> write n bytes from address
// Command of 4 -> read 32-bit character
func (c *consoleIO) TrySend(id InteractionID, command uint32, data []byte) StatusCode {
	if command == 2 {
		c.vm.stdout.WriteRune(rune(uint32FromBytes(data)))
		c.vm.stdout.Flush()
	} else if command == 3 {
		numBytes := uint32FromBytes(data)
		addr := uint32FromBytes(data[varchBytes:])
		for i := uint32(0); i < numBytes; i++ {
			b := c.vm.memory[addr+i]
			c.vm.stdout.WriteByte(b)
		}
		c.vm.stdout.Flush()
	} else if command == 4 {
		if ok := c.charRequests.send(id); !ok {
			c.ResponseBus.Send(NewResponse(c.InterruptAddr, id, nil, errIO))
			return StatusDeviceBusy
		}
	}

	return StatusDeviceReady
}

func (c *consoleIO) Reset() {
	c.Lock()
	defer c.Unlock()
	c.reset = true
}

func (c *consoleIO) Close() {
	c.charRequests.close()
	c.Lock()
	defer c.Unlock()
	c.closed = true
}
