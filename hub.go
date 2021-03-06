package redisocket

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/gorilla/websocket"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

var statistic *Statistic

//User client interface
type User interface {
	Trigger(channel string, p *Payload) (err error)
	Close()
}

//Payload receive from redis
type Payload struct {
	Len            int
	Data           []byte
	PrepareMessage *websocket.PreparedMessage
	IsPrepare      bool
	Channel        string
	AppKey         string
}

//WebsocketOptional  init websocket hub config
type WebsocketOptional struct {
	ScanInterval   time.Duration
	WriteWait      time.Duration
	PongWait       time.Duration
	PingPeriod     time.Duration
	ActivityTime   time.Duration
	MaxMessageSize int64
	Upgrader       websocket.Upgrader
}

type socketPayload struct {
	Sid  string      `json:"sid"`
	Data interface{} `json:"data"`
}

type userPayload struct {
	Uid  string      `json:"uid"`
	Data interface{} `json:"data"`
}

type reloadChannelPayload struct {
	Uid      string   `json:"uid"`
	Channels []string `json:"data"`
}

type addChannelPayload struct {
	Uid     string `json:"uid"`
	Channel string `json:"data"`
}

var (
	//DefaultWebsocketOptional default config
	DefaultWebsocketOptional = WebsocketOptional{
		ScanInterval:   30 * time.Second,
		WriteWait:      10 * time.Second,
		PongWait:       60 * time.Second,
		PingPeriod:     (60 * time.Second * 9) / 10,
		ActivityTime:   120 * time.Second,
		MaxMessageSize: 512,
		Upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
)

//EventHandler channel handler
type EventHandler func(event string, payload *Payload) error

//ReceiveMsgHandler client receive msg
type ReceiveMsgHandler func([]byte) ([]byte, error)

//NewSender return sender  send to hub
func NewSender(m *redis.Pool) (e *Sender) {
	return &Sender{
		redisManager: m,
	}
}

//Sender struct
type Sender struct {
	redisManager *redis.Pool
}

//BatchData push batch data struct
type BatchData struct {
	Channel string
	Event   string
	Data    []byte
}

//GetChannels get all sub channels
func (s *Sender) GetChannels(channelPrefix string, appKey string, pattern string) (channels []string, err error) {
	keyPrefix := fmt.Sprintf("%s%s@channels:", channelPrefix, appKey)
	conn := s.redisManager.Get()
	defer conn.Close()
	tmp, err := redis.Strings(conn.Do("keys", keyPrefix+pattern))
	channels = make([]string, 0)
	for _, v := range tmp {
		channel := strings.Replace(v, keyPrefix, "", -1)
		if channel == "" {
			continue
		}
		channels = append(channels, channel)
	}

	return
}

//GetOnlineByChannel get all online user by  channel
func (s *Sender) GetOnlineByChannel(channelPrefix string, appKey string, channel string) (online []string, err error) {
	memberKey := fmt.Sprintf("%s%s@channels:%s", channelPrefix, appKey, channel)
	conn := s.redisManager.Get()
	defer conn.Close()
	nt := time.Now().Unix()
	dt := nt - 120
	online, err = redis.Strings(conn.Do("ZRANGEBYSCORE", memberKey, dt, nt))
	return
}

//GetOnline get all online user
func (s *Sender) GetOnline(channelPrefix string, appKey string) (online []string, err error) {
	memberKey := fmt.Sprintf("%s%s@online", channelPrefix, appKey)
	conn := s.redisManager.Get()
	defer conn.Close()
	nt := time.Now().Unix()
	dt := nt - 120
	online, err = redis.Strings(conn.Do("ZRANGEBYSCORE", memberKey, dt, nt))
	return
}

//PushBatch push batch data
func (s *Sender) PushBatch(channelPrefix, appKey string, data []BatchData) {
	conn := s.redisManager.Get()
	defer conn.Close()
	for _, d := range data {
		conn.Do("PUBLISH", channelPrefix+appKey+"@"+d.Channel, d.Data)
	}
	return
}

//PushToSid  push to user socket id
func (s *Sender) PushToSid(channelPrefix, appKey string, uid string, data interface{}) (val int, err error) {
	conn := s.redisManager.Get()
	defer conn.Close()
	u := socketPayload{
		Sid:  uid,
		Data: data,
	}
	d, err := json.Marshal(u)
	if err != nil {
		return
	}
	val, err = redis.Int(conn.Do("PUBLISH", channelPrefix+appKey+"@"+"#GUSHERFUNC-TOSID#", d))
	return
}

//PushTo  push to user socket
func (s *Sender) PushToUid(channelPrefix, appKey string, uid string, data interface{}) (val int, err error) {
	conn := s.redisManager.Get()
	defer conn.Close()
	u := userPayload{
		Uid:  uid,
		Data: data,
	}
	d, err := json.Marshal(u)
	if err != nil {
		return
	}
	val, err = redis.Int(conn.Do("PUBLISH", channelPrefix+appKey+"@"+"#GUSHERFUNC-TOUID#", d))
	return
}

//ReloadChannel  reload user channel list
func (s *Sender) ReloadChannel(channelPrefix, appKey string, uid string, channels []string) (val int, err error) {
	conn := s.redisManager.Get()
	defer conn.Close()
	u := reloadChannelPayload{
		Uid:      uid,
		Channels: channels,
	}
	d, err := json.Marshal(u)
	if err != nil {
		return
	}
	val, err = redis.Int(conn.Do("PUBLISH", channelPrefix+appKey+"@"+"#GUSHERFUNC-RELOADCHANEL#", d))
	return
}

//AddChannel  append channel to user channel list
func (s *Sender) AddChannel(channelPrefix, appKey string, uid string, channel string) (val int, err error) {
	conn := s.redisManager.Get()
	defer conn.Close()
	u := addChannelPayload{
		Uid:     uid,
		Channel: channel,
	}
	d, err := json.Marshal(u)
	if err != nil {
		return
	}
	val, err = redis.Int(conn.Do("PUBLISH", channelPrefix+appKey+"@"+"#GUSHERFUNC-ADDCHANEL#", d))
	return
}

//Push push single data
func (s *Sender) Push(channelPrefix, appKey string, channel string, data []byte) (val int, err error) {
	conn := s.redisManager.Get()
	defer conn.Close()
	val, err = redis.Int(conn.Do("PUBLISH", channelPrefix+appKey+"@"+channel, data))
	return
}

//NewHub It's create a Hub
func NewHub(m *redis.Pool, log *logrus.Logger, debug bool) (e *Hub) {

	statistic = &Statistic{
		inMemChannel:  make(chan int, 8192),
		outMemChannel: make(chan int, 8192),
		inMsgChannel:  make(chan int, 8192),
		outMsgChannel: make(chan int, 8192),
		l:             log,
	}
	go statistic.Run()
	pool := &pool{
		users:              make(map[*Client]bool),
		broadcastChan:      make(chan *BroadcastPayload, 4096),
		joinChan:           make(chan *Client),
		leaveChan:          make(chan *Client),
		kickSidChan:        make(chan string),
		kickUidChan:        make(chan string),
		uPayloadChan:       make(chan *uPayload, 4096),
		uReloadChannelChan: make(chan *uReloadChannelPayload, 4096),
		uAddChannelChan:    make(chan *uAddChannelPayload, 4096),
		sPayloadChan:       make(chan *sPayload, 4096),
		shutdownChan:       make(chan int, 1),
		rpool:              m,
	}
	mq := &messageQueue{
		freeBufferChan: make(chan *buffer, 8192),
		serveChan:      make(chan *buffer, 8192),
		pool:           pool,
	}
	mq.run()

	return &Hub{

		messageQueue: mq,
		Config:       DefaultWebsocketOptional,
		redisManager: m,
		psc:          &redis.PubSubConn{Conn: m.Get()},
		pool:         pool,
		debug:        debug,
		closeSign:    make(chan int, 1),
		log:          log,
	}

}

//Upgrade gorilla websocket wrap upgrade method
func (e *Hub) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header, appKey string) (c *Client, err error) {
	ws, err := e.Config.Upgrader.Upgrade(w, r, responseHeader)
	if err != nil {
		return
	}

	auth := &Auth{}
	sid := GenSocketID()
	c = &Client{
		activityTime: time.Now(),
		appKey:       appKey,
		sid:          sid,
		ws:           ws,
		send:         make(chan *Payload, 256),
		RWMutex:      new(sync.RWMutex),
		hub:          e,
		channels:     make(map[string]bool),
		auth:         auth,
	}

	e.join(c)
	return
}

//Hub client hub
type Hub struct {
	ChannelPrefix string
	messageQueue  *messageQueue
	Config        WebsocketOptional
	psc           *redis.PubSubConn
	redisManager  *redis.Pool
	*pool
	debug     bool
	log       *logrus.Logger
	closeSign chan int
}

//Ping ping redis server
func (e *Hub) Ping() (err error) {
	_, err = e.redisManager.Get().Do("PING")
	if err != nil {
		return
	}
	return
}

func (e *Hub) logger(format string, v ...interface{}) {
	if e.debug {
		e.log.Infof(format, v...)
	}
}

//CountOnlineUsers return online user total
func (e *Hub) CountOnlineUsers() (i int) {
	return len(e.pool.users)
}

func (e *Hub) listenRedis() <-chan error {

	errChan := make(chan error, 1)
	go func() {
		for {
			switch v := e.psc.Receive().(type) {
			case redis.Message:

				//???????????????
				// v.Channel=>   ??????.app_key@channel   eg. gusher.thisisappkey@GLOBAL
				channel := strings.Replace(v.Channel, e.ChannelPrefix, "", -1)
				//?????????@ ????????????
				sch := strings.SplitN(channel, "@", 2)
				if len(sch) != 2 {
					continue
				}

				appKey := sch[0]

				//???????????????
				channel = strings.Replace(sch[1], "*", "", -1)
				if channel == "#GUSHERFUNC-TOUID#" {
					up := &userPayload{}
					err := json.Unmarshal(v.Data, up)
					if err != nil {
						continue
					}
					b, err := json.Marshal(up.Data)
					if err != nil {
						continue
					}
					e.toUid(up.Uid, b)
					continue
				}
				if channel == "#GUSHERFUNC-TOSID#" {
					up := &socketPayload{}
					err := json.Unmarshal(v.Data, up)
					if err != nil {
						continue
					}
					b, err := json.Marshal(up.Data)
					if err != nil {
						continue
					}
					e.toSid(up.Sid, b)
					continue
				}

				if channel == "#GUSHERFUNC-RELOADCHANEL#" {
					up := &reloadChannelPayload{}
					err := json.Unmarshal(v.Data, up)
					if err != nil {
						continue
					}
					e.reloadUidChannels(up.Uid, up.Channels)
					continue
				}
				if channel == "#GUSHERFUNC-ADDCHANEL#" {
					up := &addChannelPayload{}
					err := json.Unmarshal(v.Data, up)
					if err != nil {
						continue
					}
					e.addUidChannels(up.Uid, up.Channel)
					continue
				}
				pMsg, err := websocket.NewPreparedMessage(websocket.TextMessage, v.Data)
				if err != nil {
					continue
				}
				p := &Payload{
					Len:            len(v.Data),
					PrepareMessage: pMsg,
					IsPrepare:      true,

					Channel: channel,
					AppKey:  appKey,
				}
				e.broadcast(channel, p)

			case error:
				errChan <- v

				break
			}
		}
	}()
	return errChan
}

//Listen hub start
//it's block method
func (e *Hub) Listen(channelPrefix string) error {
	e.pool.channelPrefix = channelPrefix
	e.ChannelPrefix = channelPrefix
	e.psc.PSubscribe(channelPrefix + "*")
	redisErr := e.listenRedis()
	e.pool.scanInterval = e.Config.ScanInterval
	poolErr := e.pool.run()
	select {
	case er := <-redisErr:
		e.pool.shutdown()
		return er
	case er := <-poolErr:
		return er
	case <-e.closeSign:
		e.pool.shutdown()
		return nil
	}
}

//Close close hub & close every client
func (e *Hub) Close() {
	e.closeSign <- 1
	return

}

func GenSocketID() string {
	return randDigit(4) + "." + randDigit(7)
}

var digits = "0123456789"

func randDigit(n int) string {

	b := make([]byte, n)
	for i := 0; i < n; i++ {
		idx, _ := rand.Int(rand.Reader, big.NewInt(10))
		b[i] = digits[int(idx.Int64())]
	}
	return string(b)
}
