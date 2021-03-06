package redisocket

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

//Client gorilla websocket wrap struct
type Client struct {
	activityTime time.Time
	appKey       string
	channels     map[string]bool
	sid          string
	uid          string
	ws           *websocket.Conn
	send         chan *Payload
	*sync.RWMutex
	re   ReceiveMsgHandler
	hub  *Hub
	auth *Auth
}

func (c *Client) SocketId() string {
	return c.sid
}

func (c *Client) contains(a []string, x string) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}

func (c *Client) SetChannels(channels []string) {
	c.Lock()
	defer c.Unlock()
	for k, _ := range c.channels {
		if !c.contains(channels, k) {
			delete(c.channels, k)
		}
	}
}

func (c *Client) AddChannel(channel string) {
	c.Lock()
	defer c.Unlock()
	c.channels[channel] = true
}

func (c *Client) GetAuth() Auth {
	c.RLock()
	defer c.RUnlock()
	return *c.auth
}

func (c *Client) ActivityTime() {
	c.activityTime = time.Now()
}

func (c *Client) Sub(channel string) {
	c.Lock()
	c.channels[channel] = true
	c.Unlock()

	conn := c.hub.rpool.Get()
	defer conn.Close()
	nt := time.Now().Unix()
	conn.Send("MULTI")
	if c.uid != "" {
		conn.Send("ZADD", c.hub.ChannelPrefix+c.appKey+"@"+"online", "CH", nt, c.uid)
	}
	conn.Send("ZADD", c.hub.ChannelPrefix+c.appKey+"@"+"channels:"+channel, "CH", nt, c.sid)
	conn.Do("EXEC")
	return
}

//Off channel. client off channel
func (c *Client) UnSub(channel string) {
	c.Lock()
	delete(c.channels, channel)
	c.Unlock()

	conn := c.hub.rpool.Get()
	conn.Send("MULTI")
	conn.Send("ZREM", c.hub.ChannelPrefix+c.appKey+"@"+"channels:"+channel, c.sid)
	conn.Do("EXEC")
	return
}

func (c *Client) Trigger(channel string, p *Payload) (err error) {
	c.RLock()
	_, ok := c.channels[channel]
	c.RUnlock()
	if !ok {
		return errors.New("no channel")
	}

	if p.AppKey != c.appKey {
		return errors.New("no appKey")
	}

	select {
	case c.send <- p:
	default:
		c.hub.logger("socket id %s disconnect  err: trigger buffer full", c.sid)
		c.Close()
	}
	return
}

//Send message. write msg to client
func (c *Client) Send(data []byte) {
	p := &Payload{
		Len:       len(data),
		Data:      data,
		IsPrepare: false,
	}
	select {
	case c.send <- p:
	default:
		c.hub.logger("socket id %s disconnect  err: send buffer full", c.sid)
		c.Close()
	}
	return
}

func (c *Client) write(msgType int, data []byte) error {
	c.ws.SetWriteDeadline(time.Now().Add(c.hub.Config.WriteWait))
	return c.ws.WriteMessage(msgType, data)
}
func (c *Client) writePreparedMessage(data *websocket.PreparedMessage) error {
	c.ws.SetWriteDeadline(time.Now().Add(c.hub.Config.WriteWait))
	return c.ws.WritePreparedMessage(data)
}

func (c *Client) readPump() {

	defer func() {
		c.hub.leave(c)
		c.Close()

	}()
	c.ws.SetReadLimit(c.hub.Config.MaxMessageSize)
	c.ws.SetReadDeadline(time.Now().Add(c.hub.Config.PongWait))
	c.ws.SetPongHandler(func(string) error { c.ws.SetReadDeadline(time.Now().Add(c.hub.Config.PongWait)); return nil })
	for {
		msgType, reader, err := c.ws.NextReader()
		if err != nil {
			c.hub.logger("socket id %s disconnect  err: websocket read out of max message size", c.sid)
			return
		}
		if msgType != websocket.TextMessage {
			c.hub.logger("socket id %s disconnect  err: send message type not text message", c.sid)
			continue
		}

		var buf *buffer
		select {
		case buf = <-c.hub.messageQueue.freeBufferChan:
			buf.reset(c)
		default:
			// None free, so allocate a new one.
			buf = &buffer{buffer: bytes.NewBuffer(make([]byte, 0, c.hub.Config.MaxMessageSize)), client: c}
		}
		_, err = io.Copy(buf.buffer, reader)
		if err != nil {
			buf.reset(nil)
			c.hub.logger("socket id %s disconnect  err: copy buffer error", c.sid)
			return
		}
		statistic.AddInMsg(buf.buffer.Len())
		select {
		case c.hub.messageQueue.serveChan <- buf:
		default:
			c.hub.logger("socket id %s disconnect  err: server receive busy", c.sid)
			return

		}

	}

}

//Close client. disconnect client
func (c *Client) Close() {
	c.ws.Close()
	return
}

func (c *Client) writePump() {
	t := time.NewTicker(c.hub.Config.PingPeriod)
	aTime := time.NewTicker(c.hub.Config.ActivityTime)
	defer func() {
		t.Stop()
		aTime.Stop()
		c.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.hub.logger("socket id  %s disconnect  err: channel receive error", c.sid)
				return
			}

			statistic.AddOutMsg(msg.Len)
			if msg.IsPrepare {

				if err := c.writePreparedMessage(msg.PrepareMessage); err != nil {
					c.hub.logger("socket id  %s disconnect  err: write prepared message  %s", c.sid, err)
					return
				}
			} else {
				if err := c.write(websocket.TextMessage, msg.Data); err != nil {
					c.hub.logger("socket id  %s disconnect  err: write normal message  %s", c.sid, err)
					return
				}

			}

		case <-t.C:
			if err := c.write(websocket.PingMessage, []byte{}); err != nil {
				c.hub.logger("socket id  %s disconnect  err: ping message  %s", c.sid, err)
				return
			}
			//???????????? ????????????????????? ???????????????
			//if len(c.events) == 0 {
			//	c.hub.logger("socket id %s disconnect  err: timeout to subscribe", c.sid)
			//	return
			//}
		case <-aTime.C:
			if time.Now().Sub(c.activityTime) > c.hub.Config.ActivityTime {
				c.hub.logger("socket id %s disconnect  err: timeout to activity time", c.sid)
				return
			}

		}
	}

}

//Listen client
//client start listen
//it's block method
func (c *Client) Listen(re ReceiveMsgHandler) {
	c.re = re
	go c.writePump()
	c.readPump()
}
