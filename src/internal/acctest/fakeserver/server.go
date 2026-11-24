package fakeserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	DefaultUsername = "automation@example.com"
	DefaultPassword = "synthetic-test-password-not-real"
	DefaultSpaceID  = int64(1)
	DefaultVersion  = "1.10.0"
)

type Request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
	Vars   map[string]string
}

func (r *Request) JSON(out any) error { return json.Unmarshal(r.Body, out) }

func (r *Request) Map() map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(r.Body, &out)
	return out
}

type Response struct {
	Status int
	Body   any
	Header http.Header
}

func JSON(status int, body any) Response { return Response{Status: status, Body: body} }

func NoContent() Response { return Response{Status: http.StatusNoContent} }

func Detail(status int, detail string) Response {
	return Response{Status: status, Body: map[string]any{"detail": detail}}
}

type RESTHandler func(r *Request) Response

type route struct {
	method   string
	segments []string
	handler  RESTHandler
}

type failure struct {
	method string
	path   string
	status int
	left   int
}

type Server struct {
	t       testing.TB
	http    *httptest.Server
	version string
	noCheck bool
	clock   func() time.Time
	ttl     time.Duration

	mu       sync.Mutex
	routes   []route
	gql      map[string]GraphQLHandler
	log      []Request
	failures []*failure
	seq      atomic.Int64

	auth      authState
	tasks     taskState
	approvals approvalState
	tags      tagState
}

type Option func(*Server)

func WithoutSchemaValidation() Option { return func(s *Server) { s.noCheck = true } }

func WithVersion(v string) Option { return func(s *Server) { s.version = v } }

func WithClock(now func() time.Time) Option { return func(s *Server) { s.clock = now } }

func WithTokenTTL(ttl time.Duration) Option { return func(s *Server) { s.ttl = ttl } }

func New(t testing.TB, opts ...Option) *Server {
	t.Helper()
	s := &Server{t: t, version: DefaultVersion, clock: time.Now, ttl: time.Hour, gql: map[string]GraphQLHandler{}}
	for _, opt := range opts {
		opt(s)
	}
	s.initAuth()
	s.initTasks()
	s.initApprovals()
	s.initTags()
	s.registerHealth()
	s.http = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.http.Close)
	return s
}

func (s *Server) URL() string { return s.http.URL }

func (s *Server) NextID() int64 { return s.seq.Add(1) }

func (s *Server) HandleREST(method, path string, fn RESTHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes = append(s.routes, route{method: strings.ToUpper(method), segments: strings.Split(path, "/"), handler: fn})
}

func (s *Server) FailNext(method, path string, status, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, &failure{method: strings.ToUpper(method), path: path, status: status, left: times})
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.log))
	copy(out, s.log)
	return out
}

func (s *Server) RequestsFor(method, path string) []Request {
	var out []Request
	for _, r := range s.Requests() {
		if r.Method == strings.ToUpper(method) && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req := &Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: body}
	s.mu.Lock()
	s.log = append(s.log, *req)
	injected := s.consumeFailure(req)
	s.mu.Unlock()

	var resp Response
	switch {
	case injected != 0:
		resp = Detail(injected, "injected failure")
	case req.Path == graphQLRoute && req.Method == http.MethodPost:
		resp = s.serveGraphQL(req)
	default:
		resp = s.dispatchREST(req)
	}
	writeResponse(w, resp)
}

func (s *Server) consumeFailure(req *Request) int {
	for _, f := range s.failures {
		if f.left > 0 && f.method == req.Method && f.path == req.Path {
			f.left--
			return f.status
		}
	}
	return 0
}

func (s *Server) dispatchREST(req *Request) Response {
	if resp, handled := s.serveBuiltinAuth(req); handled {
		return resp
	}
	s.mu.Lock()
	routes := append([]route(nil), s.routes...)
	s.mu.Unlock()
	for i := len(routes) - 1; i >= 0; i-- {
		rt := routes[i]
		vars, ok := rt.match(req.Method, req.Path)
		if !ok {
			continue
		}
		req.Vars = vars
		if !isPublic(req.Path) {
			if denied := s.authenticate(req); denied != nil {
				return *denied
			}
		}
		return rt.handler(req)
	}
	s.t.Logf("fakeserver: no handler for %s %s", req.Method, req.Path)
	return Detail(http.StatusNotFound, "no fake handler for "+req.Method+" "+req.Path)
}

func (rt route) match(method, path string) (map[string]string, bool) {
	if rt.method != method {
		return nil, false
	}
	parts := strings.Split(path, "/")
	if len(parts) != len(rt.segments) {
		return nil, false
	}
	vars := map[string]string{}
	for i, seg := range rt.segments {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && parts[i] != "" {
			vars[seg[1:len(seg)-1]] = parts[i]
			continue
		}
		if seg != parts[i] {
			return nil, false
		}
	}
	return vars, true
}

func writeResponse(w http.ResponseWriter, resp Response) {
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	if resp.Body == nil || status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp.Body)
}

func (s *Server) registerHealth() {
	s.HandleREST(http.MethodGet, "/api/v1/health/", func(*Request) Response {
		return JSON(http.StatusOK, map[string]any{"status": "ok", "dependencies": map[string]any{"database": "ok"}, "version": s.version})
	})
}

func isPublic(path string) bool {
	return path == "/api/v1/health/"
}
