package gvm

import (
	"time"
)

type StatusCode = uint32

const (
	StatusDeviceNotFound StatusCode = 0x00
	StatusDeviceReady    StatusCode = 0x01
	StatusDeviceBusy     StatusCode = 0x02
)

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

// deviceIndex can usually be 0 unless trying to multiplex one port to multiple devices
func NewResponse(interruptAddr uint32, id InteractionID, data []byte) *Response {
	return &Response{
		interruptAddr: interruptAddr,
		id:            id,
		data:          data,
	}
}

// Begin builtin devices code
type nodevice struct {
	DeviceBaseInfo
}

type systemTimer struct {
	DeviceBaseInfo
}

type powerController struct {
	DeviceBaseInfo
	vm *VM
}

type consoleIO struct {
	DeviceBaseInfo
}

type memoryManagement struct {
	DeviceBaseInfo
	vm *VM
	// These specify the min/max addresses for the heap when in
	// a privilege mode other than 0 (highest)
	minHeapAddr uint32
	maxHeapAddr uint32
}

// ------- Begin no device marker
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
func newSystemTimer(base DeviceBaseInfo) HardwareDevice {
	return &systemTimer{
		DeviceBaseInfo: base,
	}
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

	go func(nsec time.Duration) {
		timer := time.NewTimer(nsec * time.Microsecond)
		<-timer.C
		// Use nil data in response since calling code will interpret our response
		// to mean the timer expired
		t.ResponseBus.Send(NewResponse(t.InterruptAddr, id, nil))
	}(time.Duration(uint32FromBytes(data)))

	return StatusDeviceReady
}

func (t *systemTimer) Reset() {

}

func (t *systemTimer) Close() {

}

// ------- Begin power controller
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
		// Update the CPU mode to privileged
		*p.vm.mode = 0
		// Reset program counter and stack pointer
		*p.vm.pc = reservedBytes
		*p.vm.sp = heapSizeBytes

		for _, device := range p.vm.devices {
			device.Reset()
		}

		// Set up the stack with expected initial arguments
		p.vm.pushInitialArgumentsToStack()
	} else if command == 3 {
		for _, device := range p.vm.devices {
			device.Reset()
		}

		p.vm.errcode = errSystemShutdown
	}

	return StatusDeviceReady
}

func (*powerController) Reset() {}

func (*powerController) Close() {}

// ------- Begin memory management unit
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
