package splithttp

// upload_queue is a specialized priorityqueue + channel to reorder generic
// packets by a sequence number

import (
	stderrors "errors"
	"io"
	"sync"
)

var (
	errUploadQueueClosed   = stderrors.New("xhttp upload queue closed")
	errUploadReaderExists  = stderrors.New("xhttp upload reader already exists")
	errUploadQueueTooLarge = stderrors.New("xhttp upload queue too large")
)

type Packet struct {
	Reader  io.ReadCloser
	Payload []byte
	Seq     uint64
}

type uploadQueue struct {
	mu             sync.Mutex
	cond           *sync.Cond
	reader         io.ReadCloser
	nomore         bool
	pendingPackets []Packet
	heap           uploadHeap
	nextSeq        uint64
	closed         bool
	maxPackets     int
}

func NewUploadQueue(maxPackets int) *uploadQueue {
	queue := &uploadQueue{
		heap:       uploadHeap{},
		nextSeq:    0,
		closed:     false,
		maxPackets: maxPackets,
	}
	queue.cond = sync.NewCond(&queue.mu)
	return queue
}

func (h *uploadQueue) Push(p Packet) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	for {
		if h.closed {
			return errUploadQueueClosed
		}
		if h.nomore {
			return errUploadReaderExists
		}
		if h.maxPackets > 0 && len(h.pendingPackets) >= h.maxPackets {
			h.cond.Wait()
			continue
		}
		if p.Reader != nil {
			h.nomore = true
		}
		h.pendingPackets = append(h.pendingPackets, p)
		h.cond.Signal()
		return nil
	}
}

func (h *uploadQueue) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}

	h.closed = true
	if h.reader == nil {
		for _, packet := range h.pendingPackets {
			if packet.Reader != nil {
				h.reader = packet.Reader
				break
			}
		}
	}
	reader := h.reader
	h.pendingPackets = nil
	h.cond.Broadcast()
	h.mu.Unlock()

	if reader != nil {
		return reader.Close()
	}
	return nil
}

func (h *uploadQueue) popPendingLocked() Packet {
	packet := h.pendingPackets[0]
	h.pendingPackets = h.pendingPackets[1:]
	h.cond.Signal()
	return packet
}

func (h *uploadQueue) Read(b []byte) (int, error) {
	for {
		h.mu.Lock()

		if h.reader != nil {
			reader := h.reader
			h.mu.Unlock()
			return reader.Read(b)
		}

		for len(h.pendingPackets) == 0 && (len(h.heap) == 0 || h.heap.peek().Seq > h.nextSeq) && !h.closed {
			h.cond.Wait()
			if h.reader != nil {
				reader := h.reader
				h.mu.Unlock()
				return reader.Read(b)
			}
		}

		if h.reader != nil {
			reader := h.reader
			h.mu.Unlock()
			return reader.Read(b)
		}

		if h.closed && len(h.pendingPackets) == 0 && len(h.heap) == 0 {
			h.mu.Unlock()
			return 0, io.EOF
		}

		if len(h.heap) == 0 && len(h.pendingPackets) > 0 {
			packet := h.popPendingLocked()
			if packet.Reader != nil {
				h.reader = packet.Reader
				reader := h.reader
				h.mu.Unlock()
				return reader.Read(b)
			}
			h.heap.push(packet)
		}

		packet := h.heap.peek()

		if packet.Seq < h.nextSeq {
			h.heap.pop()
			h.mu.Unlock()
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

			h.mu.Unlock()
			return n, nil
		}

		// misordered packet
		if packet.Seq > h.nextSeq {
			if len(h.heap) > h.maxPackets {
				// the "reassembly buffer" is too large, and we want to
				// constrain memory usage somehow. let's tear down the
				// connection, and hope the application retries.
				h.mu.Unlock()
				return 0, errUploadQueueTooLarge
			}
			if len(h.pendingPackets) == 0 {
				if h.closed {
					h.mu.Unlock()
					return 0, io.EOF
				}
				h.mu.Unlock()
				continue
			}
			packet2 := h.popPendingLocked()
			if packet2.Reader != nil {
				h.reader = packet2.Reader
				h.mu.Unlock()
				continue
			}
			h.heap.push(packet2)
		}

		h.mu.Unlock()
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
