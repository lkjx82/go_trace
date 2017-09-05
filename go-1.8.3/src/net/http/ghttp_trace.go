package http

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"time"
)

//
type spanSlot struct {
	spans []*traceSpan
	sync.Mutex
}

const (
	spanCellSize      = 1024
	spanExpireTimeSec = 240 // 4 min
)

type SpanTable [spanCellSize]spanSlot

//
var (
	spanTable        = SpanTable{} // [spanCellSize]spanSlot{}
	scanSpanIdx      = 0
	lastScanSpanTime = int64(0)
	enableHttpTrace  = true
)

func SetHttpTrace(enable bool) {
	enableHttpTrace = enable
}

//
func (t *SpanTable) addSpan(gid int64, span *traceSpan) {
	idx := gid % spanCellSize
	t[idx].Lock()
	if t[idx].spans == nil {
		t[idx].spans = []*traceSpan{}
	}

	for i, s := range t[idx].spans {
		if s.gid == gid {
			t[idx].spans[i] = span
			t[idx].Unlock()
			return
		}
	}
	span.gid = gid
	t[idx].spans = append(t[idx].spans, span)
	t[idx].Unlock()

	t.exprieSpan()
}

// span
func (t *SpanTable) getSpan(gid int64) (ret *traceSpan) {
	ret = nil
	idx := gid % spanCellSize
	t[idx].Lock()
	if t[idx].spans != nil {
		for _, span := range t[idx].spans {
			if span.gid == gid {
				ret = span
				break
			}
		}
	}
	t[idx].Unlock()
	return
}

//
func (t *SpanTable) exprieSpan() {
	now := time.Now().UnixNano()
	if now-lastScanSpanTime < 1e6*60 {
		return
	}
	lastScanSpanTime = now
	idx := scanSpanIdx % spanCellSize
	scanSpanIdx++

	spanTable[idx].Lock()
	defer spanTable[idx].Unlock()
	if spanTable[idx].spans == nil {
		return
	}

	for i := 0; i < len(spanTable[idx].spans); i++ {
		if now-spanTable[idx].spans[i].Timestamp > 1e6*240 {
			spanTable[idx].spans = append(spanTable[idx].spans[:i], spanTable[idx].spans[:i+1]...)
			i--
		}
	}
}

/*
//
var (
	spansMap gidSpanMapTyp = gidSpanMapTyp{spans: make(map[int64]*traceSpan)}
)

// map[gid]*span
type gidSpanMapTyp struct {
	sync.Mutex
	spans map[int64]*traceSpan
}

// add span
func (m *gidSpanMapTyp) AddSpan(gid int64, span *traceSpan) {
	m.Lock()
	if _, ok := m.spans[gid]; ok {
		delete(m.spans, gid)
		panic(fmt.Sprintf("不应该有的Span %d", gid))
	}
	m.spans[gid] = span
	m.Unlock()
}

// get span from map
func (m *gidSpanMapTyp) GetSpan(gid int64) *traceSpan {
	m.Lock()
	defer m.Unlock()
	if span, ok := m.spans[gid]; ok {
		return span
	}
	return nil
}
*/

// 从协程链里找到接受req的协程
func getSpanByPG() *traceSpan {
	gid := runtime.Getgid()

	pgids := make([]int64, 100)
	n := runtime.Getgpid(gid, pgids)
	for i := 0; i < n; i++ {
		pid := pgids[i]
		// if span := spansMap.GetSpan(pid); span != nil {
		if span := spanTable.getSpan(pid); span != nil {
			if span.isRecvReq {
				return span
			}
		}
	}
	return nil
}

// 接受请求
// 从 recv 的 req 里解析span，记录下来,  SR
func onHttpProcRecvReq(req *Request) *traceSpan {
	if !enableHttpTrace {
		return nil
	}
	span := newTraceSpan()
	span.fromHeader(req.Header)
	span.Path = req.URL.String()
	span.Name = req.Method

	// sr
	ep := &endpoint{Ipv4: localIpv4, ServiceName: execName, Port: 80}
	span.addAnnotation(ep, getTraceTime(), "sr")
	span.isRecvReq = true

	// url, method
	span.addBinAnnotation(ep, "http.url", req.URL.String())
	span.addBinAnnotation(ep, "http.method", req.Method)

	// add ca
	epRemote := &endpoint{ServiceName: execName}
	epRemote.Ipv4, epRemote.Port = getAddrFromString(req.RemoteAddr)
	span.addBinAnnotation(epRemote, "ca", "true")

	// add to map
	gid := runtime.Getgid()
	// spansMap.AddSpan(gid, span)
	spanTable.addSpan(gid, span)
	return span
}

// 相应接受请求
// 发送 respone 给请求方, SS
func onHttpSendResp(resp *response, span *traceSpan) {
	if !enableHttpTrace {
		return
	}
	// span := getSpanByPG()
	if span != nil {
		ep := &endpoint{Ipv4: localIpv4, ServiceName: execName, Port: 80}
		span.addAnnotation(ep, getTraceTime(), "ss")
		span.Duration = getTraceTime() - span.Timestamp

		b, _ := json.MarshalIndent(span, "", "\t")
		fmt.Println(string(b))

		logTrace(span)
	} else {
		span := newTraceSpan()
		span.fromHeader(resp.req.Header)
		span.Path = resp.req.RequestURI
		span.Name = resp.req.Method
		panic("not here")
	}
}

//
func onHttpServerErr(req *Request, span *traceSpan, err error) {
	if !enableHttpTrace {
		return
	}
	ep := &endpoint{Ipv4: localIpv4, ServiceName: execName, Port: 80}
	span.addBinAnnotation(ep, "error", err.Error())
	span.Duration = getTraceTime() - span.Timestamp

	span.addAnnotation(ep, getTraceTime(), "ss")

	b, _ := json.MarshalIndent(span, "", "\t")
	fmt.Println(string(b))
	logTrace(span)
}

// ------------------------------------------------------------------------------------
// 发送请求

// 接受回应
// 发送 request 前，记录道span, CS
func onHttpSendReq(req *Request) *traceSpan {
	if !enableHttpTrace {
		return nil
	}
	parentSpan := getSpanByPG()
	span := newTraceSpan()
	span.SpanId = genSpanId()
	span.Path = req.URL.String()
	span.Name = req.Method
	span.Timestamp = getTraceTime()

	ep := &endpoint{Ipv4: localIpv4, ServiceName: execName, Port: 80}
	span.addAnnotation(ep, getTraceTime(), "cs")

	if parentSpan != nil { // 找到 sr，就是 req 的 trace 的上层
		span.fromParentSpan(parentSpan)
		parentSpan.addChildSpan(span)
	} else { // 找到
		span.TraceId = genSpanId()
	}
	span.setHeader(req.Header)

	span.addBinAnnotation(ep, "http.url", req.URL.String())
	span.addBinAnnotation(ep, "http.method", req.Method)

	return span
}

// 接收到 respone, CR
func onHttpRecvResp(resp *Response, span *traceSpan) {
	if !enableHttpTrace {
		return
	}
	ep := &endpoint{Ipv4: localIpv4, ServiceName: execName, Port: 80}
	span.addAnnotation(ep, getTraceTime(), "cr")
	span.addBinAnnotation(ep, "http.status_code", strconv.Itoa(resp.StatusCode))
	span.Duration = getTraceTime() - span.Timestamp

	b, _ := json.MarshalIndent(span, "", "\t")
	fmt.Println(string(b))

	logTrace(span)
}

//
func onHttpClientErr(req *Request, span *traceSpan, err error) {
	if !enableHttpTrace {
		return
	}
	ep := &endpoint{Ipv4: localIpv4, ServiceName: execName, Port: 80}
	span.addBinAnnotation(ep, "error", err.Error())
	span.Duration = getTraceTime() - span.Timestamp

	span.addAnnotation(ep, getTraceTime(), "cr")

	b, _ := json.MarshalIndent(span, "", "\t")
	fmt.Println(string(b))
	logTrace(span)
}

//
