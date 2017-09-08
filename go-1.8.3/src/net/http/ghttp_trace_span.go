package http

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	localIpv4 = "unknown"
	execName  = "unknown"
)

const (
	PROBE_VER       = "0.1"
	FIELD_TRACE_ID  = "X-W-TraceId"
	FIELD_SPAN_ID   = "X-W-SpanId"
	FIELD_PARENT_ID = "X-W-ParentId"
	FIELD_SIMPLE    = "X-W-Sample"
	FIELD_FLAGS     = "X-W-Flags"
)

//
type endpoint struct {
	ServiceName string `json:"serviceName"` // require
	Ipv4        string `json:"ipv4"`        // require
	Port        uint16 `json:"port"`        // require
}

//
type annotation struct {
	Endpoint  endpoint `json:"endpoint"`
	Timestamp int64    `json:"timestamp"`
	Value     string   `json:"value"` // request, event  sr, ss, cr, cs, error
}

//
// ca:client addr, sa:server addr, http.status_code(200,500,404), http.method(get,post),http.url,
// error: timeout
// db.instance: db name, db.statement:sql
// db.type	全小写，"mysql", "cassandra", "hbase", or "redis"
type binAnnotation struct {
	Endpoint endpoint `json:"endpoint"`
	Key      string   `json:"key"`
	Value    string   `json:"value"` //
}

//
type traceSpan struct {
	TraceId       string          `json:"traceId"`            // required
	ParentId      string          `json:"parentId,omitempty"` // required
	SpanId        string          `json:"id"`                 // required
	Name          string          `json:"name"`               // span name, like get, default unknown, in mysql is func name
	Path          string          `json:"path,omitempty"`     // go src file line?
	Timestamp     int64           `json:"timestamp"`          // req start time
	Duration      int64           `json:"duration"`           // span use time
	Version       string          `json:"version"`            // go 1.8.3
	Annotation    []annotation    `json:"annotations"`
	BinAnnotation []binAnnotation `json:"binaryAnnotations"`
	childSpans    []*traceSpan    `json:"-"`
	flags         string          `json:"-"`
	isSample      bool            `json:"-"`
	isRecvReq     bool            `json:"-"`
	gid           int64           `json:"-"`
	localPort     uint16          `json:"-"` // for server
	sync.Mutex    `json:"-"`
}

// 从 header 设置 span
func (s *traceSpan) setHeader(h Header) {
	h.Set(FIELD_TRACE_ID, s.TraceId)
	h.Set(FIELD_SPAN_ID, s.SpanId)
	h.Set(FIELD_PARENT_ID, s.ParentId)
	if s.isSample {
		h.Set(FIELD_SIMPLE, "true")
	} else {
		h.Set(FIELD_SIMPLE, "false")
	}
	h.Set(FIELD_FLAGS, s.flags)
}

// 从 header 设置 span
func (s *traceSpan) fromHeader(h Header) {
	s.TraceId = h.Get(FIELD_TRACE_ID)
	s.SpanId = h.Get(FIELD_SPAN_ID)
	s.ParentId = h.Get(FIELD_PARENT_ID)
	s.isSample = true
	if h.Get(FIELD_SPAN_ID) == "false" {
		s.isSample = false
	}
	s.flags = h.Get(FIELD_FLAGS)
}

// 从父 span 拷贝数据
func (s *traceSpan) fromParentSpan(span *traceSpan) {
	s.TraceId = span.TraceId
	s.ParentId = span.SpanId
	s.isSample = span.isSample
	s.flags = span.flags
}

//
func (s *traceSpan) addAnnotation(ep *endpoint, ts int64, value string) {
	if s.Annotation == nil {
		s.Annotation = []annotation{
			annotation{
				Endpoint:  *ep,
				Timestamp: ts,
				Value:     value,
			},
		}
	} else {
		s.Annotation = append(s.Annotation,
			annotation{
				Endpoint:  *ep,
				Timestamp: ts,
				Value:     value,
			})
	}
}

//
func (s *traceSpan) addBinAnnotation(ep *endpoint, key, value string) {
	if s.BinAnnotation == nil {
		s.BinAnnotation = []binAnnotation{
			binAnnotation{
				Endpoint: *ep,
				Key:      key,
				Value:    value,
			},
		}
	} else {
		s.BinAnnotation = append(s.BinAnnotation,
			binAnnotation{
				Endpoint: *ep,
				Key:      key,
				Value:    value,
			})
	}
}

//
func newEndpoint(srvName, ip string, port uint16) *endpoint {
	return &endpoint{
		ServiceName: srvName,
		Ipv4:        ip,
		Port:        port,
	}
}

//
func newTraceSpan() *traceSpan {
	s := traceSpan{
		Timestamp: getTraceTime(),
		Version:   PROBE_VER,
	}
	return &s
}

// 把s2加到s1后面
func (s *traceSpan) addChildSpan(s2 *traceSpan) {
	s.Lock()
	defer s.Unlock()
	for _, v := range s.childSpans {
		if v.SpanId == s2.SpanId {
			return
		}
	}
	s.childSpans = append(s.childSpans, s2)
}

//
func genSpanId() string {
	t := time.Now().UnixNano()
	return fmt.Sprintf("%x", t)
}

//
func getTraceTime() int64 {
	t := time.Now()
	return t.UnixNano() / 1e3
}

//
// func getAddrFromRespHeader(h Header) (ip string, port int16) {
// addr := h.Get("Remote Address")
// return getAddrFromString(addr)
// }

//
func getAddrFromString(addr string) (ip string, port uint16) {
	s := strings.Split(addr, ":")
	ip = s[0]
	port = uint16(80)
	fmt.Printf("getAddrFromString : %##v\n", s)
	if len(s) > 1 {
		if port64, err := strconv.ParseUint(s[1], 10, 16); err == nil {
			port = uint16(port64)
			fmt.Println(port, port64)
		} else {
			fmt.Println(err, port, port64)
		}
	}
	return
}

//
func init() {
	initLocalIpv4()
	initExecName()
}

//
func initExecName() {
	idx := strings.LastIndex(os.Args[0], "/")
	if idx >= 0 {
		execName = os.Args[0][idx+1:]
	} else {
		execName = os.Args[0]
	}
}

//
func initLocalIpv4() {
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.IsGlobalUnicast() {
				if ipnet.IP.To4() != nil {
					localIpv4 = ipnet.IP.String()
				}
			}
		}
	}
}

//
var (
	spansChan = make(chan *traceSpan, 1000)
)

func logTrace(span *traceSpan) {
	spansChan <- span
}

func init() {

	go func() {
		traceSpanCache := [1024]*traceSpan{}
		idx := 0
		t := time.NewTicker(time.Second * 10)

		flushFunc := func() {
			if idx <= 0 {
				return
			}
			if b, err := json.Marshal(traceSpanCache[:idx]); err == nil {

				if f, err := os.OpenFile(fmt.Sprintf("./trace_%s_%d.txt", time.Now().Format("2006-01-02"), os.Getpid()),
					os.O_APPEND|os.O_RDWR|os.O_CREATE,
					0755); err == nil {
					f.Write(b)
					f.Close()
				} else {
					panic(err)
				}
				idx = 0
			} else {
				panic(err)
			}
		}

		for {
			select {
			case span := <-spansChan:
				if idx >= 1024 {
					flushFunc()
				}
				traceSpanCache[idx] = span
				idx++
			case <-t.C:
				flushFunc()
			}
		}
	}()
}

//
