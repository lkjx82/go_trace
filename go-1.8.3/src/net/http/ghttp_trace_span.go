package http

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
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
	Port        int16  `json:"port"`        // require
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
// [language].xx.xx:	针对于不同语言的一些自定义的内容, exp. php.db.source
type binAnnotation struct {
	Endpoint endpoint `json:"endpoint"`
	Key      string   `json:"key"`
	Value    string   `json:"value"` //
}

//
type traceSpan struct {
	TraceId       string          `json:"traceId"`   // required
	ParentId      string          `json:"parentId"`  // required
	SpanId        string          `json:"id"`        // required
	Name          string          `json:"name"`      // span name, like get, default unknown, in mysql is func name
	Path          string          `json:"path"`      // go src file line?
	Timestamp     int64           `json:"timestamp"` // req start time
	Duration      int64           `json:"duration"`  // span use time
	Version       string          `json:"version"`   // go 1.8.3
	Annotation    []annotation    `json:"annotations"`
	BinAnnotation []binAnnotation `json:"binaryAnnotations"`
	childSpans    []*traceSpan    `json:"-"`
	flags         string          `json:"-"`
	isSample      bool            `json:"-"`
	isRecvReq     bool            `json:"-"`
	// next          *traceSpan      `json:"-"`
	// prev          *traceSpan      `json:"-"`
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
func newEndpoint(srvName, ip string, port int16) *endpoint {
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
	for _, v := range s.childSpans {
		if v.SpanId == s2.SpanId {
			return
		}
	}
	s.childSpans = append(s.childSpans, s2)
	// if s.next != nil {
	// s2.next = s.next
	// s.next.prev = s2
	// }
	// s.next = s2
	// s2.prev = s
}

// 从链表中删除自己
func (s *traceSpan) rmSelf() {
	// for idx, v := range s.childSpans {
	// if v.SpanId == s.SpanId {
	// s.childSpans = append(s.childSpans[:idx], s.childSpans[idx+1:]...)
	// }
	// }
}

//
func genSpanId() string {
	return fmt.Sprintf("spanid-%d", time.Now().UnixNano())
}

//
func getTraceTime() int64 {
	t := time.Now()
	return t.UnixNano() / 1e3
}

//
func getAddrFromRespHeader(h Header) (ip string, port int16) {
	addr := h.Get("Remote Address")
	return getAddrFromString(addr)
}

//
func getAddrFromString(addr string) (ip string, port int16) {
	s := strings.Split(addr, ":")
	ip = s[0]
	port = 80
	if len(s) > 1 {
		if port64, err := strconv.ParseInt(s[1], 10, 16); err != nil {
			port = int16(port64)
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
