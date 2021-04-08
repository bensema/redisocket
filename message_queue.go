package redisocket

import (
	"log"
)

type messageQueue struct {
	serveChan      chan *buffer
	freeBufferChan chan *buffer
	pool           *pool
}

func (m *messageQueue) worker() {
	for {
		select {
		case b := <-m.serveChan:
			m.serve(b)
		}
	}
	log.Println("[redisocket] message queue crash")
}
func (m *messageQueue) run() {
	for i := 0; i < 1024; i++ {
		go m.worker()
	}
}

func (m *messageQueue) serve(buffer *buffer) {
	receiveMsg, err := buffer.client.re(buffer.buffer.Bytes())
	if err == nil {
		byteCount := len(receiveMsg)
		if byteCount > 0 {
			m.pool.toSid(buffer.client.sid, receiveMsg)
		}
	} else {
		m.pool.kickSid(buffer.client.sid)
	}
	buffer.reset(nil)
	select {
	case m.freeBufferChan <- buffer:
	default:
	}
	return
}
