package splithttp

// upload_queue is a specialized priorityqueue + channel to reorder generic
// packets by a sequence number

import (
	"io"
	"runtime"
	"sync"

	"github.com/xtls/xray-core/common/errors"
)

type Packet struct {
	Reader  io.ReadCloser
	Payload []byte
	Seq     uint64
}

type uploadQueue struct {
	reader          io.ReadCloser
	nomore          bool
	pushedPackets   chan Packet
	writeCloseMutex sync.Mutex
	heap            uploadHeap
	nextSeq         uint64
	closed          bool
	maxPackets      int
}

func NewUploadQueue(maxPackets int) *uploadQueue {
	return &uploadQueue{
		pushedPackets: make(chan Packet, maxPackets),
		heap:          uploadHeap{},
		nextSeq:       0,
		closed:        false,
		maxPackets:    maxPackets,
	}
}

func (h *uploadQueue) Push(p Packet) error {
	h.writeCloseMutex.Lock()
	defer h.writeCloseMutex.Unlock()

	if h.closed {
		return errors.New("packet queue closed")
	}
	if h.nomore {
		return errors.New("h.reader already exists")
	}
	if p.Reader != nil {
		h.nomore = true
	}
	h.pushedPackets <- p
	return nil
}

func (h *uploadQueue) Close() error {
	h.writeCloseMutex.Lock()
	defer h.writeCloseMutex.Unlock()

	if !h.closed {
		h.closed = true
		runtime.Gosched() // hope Read() gets the packet
	f:
		for {
			select {
			case p := <-h.pushedPackets:
				if p.Reader != nil {
					h.reader = p.Reader
				}
			default:
				break f
			}
		}
		close(h.pushedPackets)
	}
	if h.reader != nil {
		return h.reader.Close()
	}
	return nil
}

func (h *uploadQueue) Read(b []byte) (int, error) {
	if h.reader != nil {
		return h.reader.Read(b)
	}

	if h.closed {
		return 0, io.EOF
	}

	for {
		if h.reader != nil {
			return h.reader.Read(b)
		}
		if len(h.heap) == 0 {
			packet, more := <-h.pushedPackets
			if !more {
				return 0, io.EOF
			}
			if packet.Reader != nil {
				h.reader = packet.Reader
				return h.reader.Read(b)
			}
			h.heap.push(packet)
		}

		packet := h.heap.peek()

		if packet.Seq < h.nextSeq {
			h.heap.pop()
			continue
		}

		if packet.Seq == h.nextSeq {
			packet = h.heap.pop()
			n := copy(b, packet.Payload)

			if n < len(packet.Payload) {
				// partial read
				packet.Payload = packet.Payload[n:]
				h.heap.push(packet)
			} else {
				h.nextSeq = packet.Seq + 1
			}

			return n, nil
		}

		// misordered packet
		if packet.Seq > h.nextSeq {
			if len(h.heap) > h.maxPackets {
				// the "reassembly buffer" is too large, and we want to
				// constrain memory usage somehow. let's tear down the
				// connection, and hope the application retries.
				return 0, errors.New("packet queue is too large")
			}
			packet2, more := <-h.pushedPackets
			if !more {
				return 0, io.EOF
			}
			if packet2.Reader != nil {
				h.reader = packet2.Reader
				continue
			}
			h.heap.push(packet2)
		}
	}
}

type uploadHeap []Packet

func (h uploadHeap) peek() Packet {
	return h[0]
}

func (h *uploadHeap) push(packet Packet) {
	*h = append(*h, packet)
	for i := len(*h) - 1; i > 0; {
		parent := (i - 1) / 2
		if (*h)[parent].Seq <= (*h)[i].Seq {
			break
		}
		(*h)[parent], (*h)[i] = (*h)[i], (*h)[parent]
		i = parent
	}
}

func (h *uploadHeap) pop() Packet {
	last := len(*h) - 1
	packet := (*h)[0]
	if last == 0 {
		*h = (*h)[:0]
		return packet
	}

	(*h)[0] = (*h)[last]
	*h = (*h)[:last]

	for i := 0; ; {
		left := i*2 + 1
		if left >= len(*h) {
			break
		}

		smallest := left
		right := left + 1
		if right < len(*h) && (*h)[right].Seq < (*h)[left].Seq {
			smallest = right
		}
		if (*h)[i].Seq <= (*h)[smallest].Seq {
			break
		}
		(*h)[i], (*h)[smallest] = (*h)[smallest], (*h)[i]
		i = smallest
	}

	return packet
}
