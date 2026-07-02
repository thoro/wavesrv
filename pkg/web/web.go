// Copyright 2025, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/wavetermdev/waveterm/pkg/aiusechat"
	"github.com/wavetermdev/waveterm/pkg/authkey"
	"github.com/wavetermdev/waveterm/pkg/filestore"
	"github.com/wavetermdev/waveterm/pkg/panichandler"
	"github.com/wavetermdev/waveterm/pkg/remote/fileshare/wshfs"
	"github.com/wavetermdev/waveterm/pkg/schema"
	"github.com/wavetermdev/waveterm/pkg/service"
	"github.com/wavetermdev/waveterm/pkg/util/ansistrip"
	"github.com/wavetermdev/waveterm/pkg/util/fileutil"
	"github.com/wavetermdev/waveterm/pkg/wavebase"
	"github.com/wavetermdev/waveterm/pkg/wshrpc"
	"github.com/wavetermdev/waveterm/pkg/wshrpc/wshclient"
)

type WebFnType = func(http.ResponseWriter, *http.Request)

const TransparentGif64 = "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"

// Header constants
const (
	CacheControlHeaderKey     = "Cache-Control"
	CacheControlHeaderNoCache = "no-cache"

	ContentTypeHeaderKey = "Content-Type"
	ContentTypeJson      = "application/json"
	ContentTypeBinary    = "application/octet-stream"

	ContentLengthHeaderKey = "Content-Length"
	LastModifiedHeaderKey  = "Last-Modified"

	WaveZoneFileInfoHeaderKey = "X-ZoneFileInfo"
	WaveLineInfoHeaderKey     = "X-WaveLineInfo"
)

const HttpReadTimeout = 5 * time.Second

// Unix-socket serving talks only to our own trusted local client (0700 socket, no
// slow-loris exposure). A heavily loaded client process can connect early and get
// its request write scheduled seconds later; a 5s read deadline then closes the
// connection with a zero-byte response.
const HttpUnixReadTimeout = 60 * time.Second
const HttpWriteTimeout = 21 * time.Second
const HttpMaxHeaderBytes = 60000
const HttpTimeoutDuration = 21 * time.Second

const WSStateReconnectTime = 30 * time.Second
const WSStatePacketChSize = 20

type WebFnOpts struct {
	AllowCaching bool
	JsonErrors   bool
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

type notFoundBlockingResponseWriter struct {
	w       http.ResponseWriter
	status  int
	headers http.Header
}

func (rw *notFoundBlockingResponseWriter) Header() http.Header {
	return rw.headers
}

func (rw *notFoundBlockingResponseWriter) WriteHeader(status int) {
	if status == http.StatusNotFound {
		rw.status = status
		return
	}
	rw.status = status
	copyHeaders(rw.w.Header(), rw.headers)
	rw.w.WriteHeader(status)
}

func (rw *notFoundBlockingResponseWriter) Write(b []byte) (int, error) {
	if rw.status == http.StatusNotFound {
		// Block the write if it's a 404
		return len(b), nil
	}
	if rw.status == 0 {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.w.Write(b)
}

func handleService(w http.ResponseWriter, r *http.Request) {
	bodyData, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Unable to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	var webCall service.WebCallType
	err = json.Unmarshal(bodyData, &webCall)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
	}

	rtn := service.CallService(r.Context(), webCall)
	jsonRtn, err := json.Marshal(rtn)
	if err != nil {
		http.Error(w, fmt.Sprintf("error serializing response: %v", err), http.StatusInternalServerError)
	}
	w.Header().Set(ContentTypeHeaderKey, ContentTypeJson)
	w.Header().Set(ContentLengthHeaderKey, fmt.Sprintf("%d", len(jsonRtn)))
	w.WriteHeader(http.StatusOK)
	w.Write(jsonRtn)
}

func marshalReturnValue(data any, err error) []byte {
	var mapRtn = make(map[string]any)
	if err != nil {
		mapRtn["error"] = err.Error()
	} else {
		mapRtn["success"] = true
		mapRtn["data"] = data
	}
	rtn, err := json.Marshal(mapRtn)
	if err != nil {
		return marshalReturnValue(nil, fmt.Errorf("error serializing response: %v", err))
	}
	return rtn
}

func queryInt64(r *http.Request, key string) (int64, error) {
	str := r.URL.Query().Get(key)
	if str == "" {
		return 0, nil
	}
	val, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return val, nil
}

// serves a byte range of a wave file. the range can be selected four ways:
//   - offset: stream from max(offset, DataStartIdx) to EOF
//   - offset + count: stream at most count bytes from there
//   - tail: stream the last tail retained bytes
//   - startline + numlines: stream the byte range covering those logical lines
//     (resolved via the filestore line index; X-WaveLineInfo returns
//     "startByte:endByte:startLine")
//
// stripansi=1 streams the range as ANSI-stripped plain text (one line per
// logical line) instead of raw VT bytes -- the terminal's full-buffer copy.
func handleWaveFile(w http.ResponseWriter, r *http.Request) {
	zoneId := r.URL.Query().Get("zoneid")
	name := r.URL.Query().Get("name")
	var offset, count, tail, startLine, numLines, stripAnsi int64
	var parseErr error
	for _, p := range []struct {
		key string
		val *int64
	}{{"offset", &offset}, {"count", &count}, {"tail", &tail}, {"startline", &startLine}, {"numlines", &numLines}, {"stripansi", &stripAnsi}} {
		if *p.val, parseErr = queryInt64(r, p.key); parseErr != nil {
			http.Error(w, parseErr.Error(), http.StatusBadRequest)
			return
		}
	}
	if _, err := uuid.Parse(zoneId); err != nil {
		http.Error(w, fmt.Sprintf("invalid zoneid: %v", err), http.StatusBadRequest)
		return
	}
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return

	}
	file, err := filestore.WFS.Stat(r.Context(), zoneId, name)
	if err == fs.ErrNotExist {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("error getting file info: %v", err), http.StatusInternalServerError)
		return
	}
	jsonFileBArr, err := json.Marshal(file)
	if err != nil {
		http.Error(w, fmt.Sprintf("error serializing file info: %v", err), http.StatusInternalServerError)
	}
	// can make more efficient by checking modtime + If-Modified-Since headers to allow caching
	dataStartIdx := file.DataStartIdx()
	if numLines > 0 {
		lineStart, lineEnd, err := filestore.WFS.LineRangeToByteRange(r.Context(), zoneId, name, startLine, numLines)
		if err != nil {
			http.Error(w, fmt.Sprintf("error resolving line range: %v", err), http.StatusInternalServerError)
			return
		}
		dataStartIdx = lineStart
		count = lineEnd - lineStart
		setLineInfoHeader(r, w, zoneId, name, lineStart, lineEnd, startLine)
	} else if tail > 0 {
		if start := file.Size - tail; start > dataStartIdx {
			// align the tail to the next line start so the client's history/live
			// seam begins at a whole logical line (no partial-row duplication)
			dataStartIdx = alignToNextLine(r.Context(), zoneId, name, start, file.Size)
		}
		// line info lets the terminal history layer size its line-space scroll
		// without a second round trip (best-effort: circular files have no index)
		if bodyStartLine, err := filestore.WFS.OffsetToLine(r.Context(), zoneId, name, dataStartIdx); err == nil {
			setLineInfoHeader(r, w, zoneId, name, dataStartIdx, file.Size, bodyStartLine)
		}
	} else if offset >= dataStartIdx {
		dataStartIdx = offset
	}
	endIdx := file.Size
	if count > 0 && dataStartIdx+count < endIdx {
		endIdx = dataStartIdx + count
	}
	w.Header().Set(ContentTypeHeaderKey, ContentTypeBinary)
	if stripAnsi == 0 {
		// stripped output length is unknown up front (chunked transfer)
		w.Header().Set(ContentLengthHeaderKey, fmt.Sprintf("%d", maxInt64(endIdx-dataStartIdx, 0)))
	}
	w.Header().Set(WaveZoneFileInfoHeaderKey, base64.StdEncoding.EncodeToString(jsonFileBArr))
	w.Header().Set(LastModifiedHeaderKey, time.UnixMilli(file.ModTs).UTC().Format(http.TimeFormat))
	if dataStartIdx >= endIdx {
		w.WriteHeader(http.StatusOK)
		return
	}
	var scanner *ansistrip.LineScanner
	if stripAnsi != 0 {
		scanner = ansistrip.NewLineScanner(dataStartIdx)
	}
	for offset := dataStartIdx; offset < endIdx; offset += filestore.DefaultPartDataSize {
		readSize := minInt64(filestore.DefaultPartDataSize, endIdx-offset)
		_, data, err := filestore.WFS.ReadAt(r.Context(), zoneId, name, offset, readSize)
		if err != nil {
			if offset == 0 {
				http.Error(w, fmt.Sprintf("error reading file: %v", err), http.StatusInternalServerError)
			} else {
				// nothing to do, the headers have already been sent
				log.Printf("error reading file %s/%s @ %d: %v\n", zoneId, name, offset, err)
			}
			return
		}
		if scanner == nil {
			w.Write(data)
			continue
		}
		for _, line := range scanner.Feed(data) {
			w.Write([]byte(line.Text))
			w.Write([]byte{'\n'})
		}
	}
	if scanner != nil {
		if last := scanner.Flush(); last != nil {
			w.Write([]byte(last.Text))
			w.Write([]byte{'\n'})
		}
	}
}

// returns the byte offset just past the first newline at/after start (searching
// at most one part), or start unchanged when none is found (pathological
// no-newline data -- the client accepts a mid-line seam then).
func alignToNextLine(ctx context.Context, zoneId string, name string, start int64, size int64) int64 {
	readSize := minInt64(filestore.DefaultPartDataSize, size-start)
	if readSize <= 0 {
		return start
	}
	_, data, err := filestore.WFS.ReadAt(ctx, zoneId, name, start, readSize)
	if err != nil {
		return start
	}
	for i, b := range data {
		if b == '\n' {
			return start + int64(i) + 1
		}
	}
	return start
}

// X-WaveLineInfo: "startByte:endByte:startLine:totalLines" -- the resolved
// byte range of a line-addressed (or tail) read plus the file's total flushed
// line count, in retained-relative raw-\n line space.
func setLineInfoHeader(r *http.Request, w http.ResponseWriter, zoneId string, name string, startByte int64, endByte int64, startLine int64) {
	totalLines, err := filestore.WFS.GetTotalLineCount(r.Context(), zoneId, name)
	if err != nil {
		return
	}
	w.Header().Set(WaveLineInfoHeaderKey, fmt.Sprintf("%d:%d:%d:%d", startByte, endByte, startLine, totalLines))
}

func minInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func serveTransparentGIF(w http.ResponseWriter) {
	gifBytes, _ := base64.StdEncoding.DecodeString(TransparentGif64)
	w.Header().Set("Content-Type", "image/gif")
	w.WriteHeader(http.StatusOK)
	w.Write(gifBytes)
}

func handleLocalStreamFile(w http.ResponseWriter, r *http.Request, path string, no404 bool) {
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	if no404 {
		log.Printf("streaming file w/no404: %q\n", path)
		// use the custom response writer
		rw := &notFoundBlockingResponseWriter{w: w, headers: http.Header{}}

		// Serve the file using http.ServeFile
		path, err := wavebase.ExpandHomeDir(path)
		if err == nil {
			http.ServeFile(rw, r, filepath.Clean(path))
			// if the file was not found, serve the transparent GIF
			log.Printf("got streamfile status: %d\n", rw.status)
			if rw.status == http.StatusNotFound {
				serveTransparentGIF(w)
			}
		} else {
			serveTransparentGIF(w)
		}
	} else {
		path, err := wavebase.ExpandHomeDir(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		http.ServeFile(w, r, path)
	}
}

func handleStreamFileFromReader(w http.ResponseWriter, r *http.Request, path string, no404 bool) error {
	startTime := time.Now()
	rangeHeader := r.Header.Get("Range")
	log.Printf("stream-file path=%q range=%q\n", path, rangeHeader)

	writerRouteId, err := wshfs.GetConnectionRouteId(r.Context(), path)
	if err != nil {
		return err
	}

	byteRange := ""
	if rangeHeader != "" {
		stripped := strings.TrimPrefix(rangeHeader, "bytes=")
		br, parseErr := fileutil.ParseByteRange(stripped)
		if parseErr != nil || br.All {
			http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
			return nil
		}
		byteRange = stripped
	}

	bareRpc := wshclient.GetBareRpcClient()
	readerRouteId := wshclient.GetBareRpcClientRouteId()
	reader, streamMeta := bareRpc.StreamBroker.CreateStreamReader(readerRouteId, writerRouteId, 256*1024)
	defer reader.Close()
	go func() {
		<-r.Context().Done()
		reader.Close()
	}()

	data := wshrpc.CommandFileStreamData{
		Info:       &wshrpc.FileInfo{Path: path},
		ByteRange:  byteRange,
		StreamMeta: *streamMeta,
	}
	fileInfo, err := wshfs.FileStream(r.Context(), data)
	if err != nil {
		if no404 {
			serveTransparentGIF(w)
			return nil
		}
		return err
	}
	if fileInfo.NotFound {
		if no404 {
			serveTransparentGIF(w)
			return nil
		}
		http.Error(w, fmt.Sprintf("file not found: %q", path), http.StatusNotFound)
		return nil
	}
	if fileInfo.IsDir {
		http.Error(w, fmt.Sprintf("cannot stream directory: %q", path), http.StatusBadRequest)
		return nil
	}
	log.Printf("stream-file headers-ready path=%q time-to-headers=%v\n", path, time.Since(startTime))
	w.Header().Set(ContentTypeHeaderKey, fileInfo.MimeType)
	w.Header().Set("Accept-Ranges", "bytes")
	if byteRange != "" {
		br, _ := fileutil.ParseByteRange(byteRange)
		var rangeEnd int64
		if br.OpenEnd {
			rangeEnd = fileInfo.Size - 1
		} else {
			rangeEnd = br.End
		}
		w.Header().Set(ContentLengthHeaderKey, fmt.Sprintf("%d", rangeEnd-br.Start+1))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", br.Start, rangeEnd, fileInfo.Size))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set(ContentLengthHeaderKey, fmt.Sprintf("%d", fileInfo.Size))
	}
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	_, copyErr := io.Copy(w, reader)
	if copyErr != nil && r.Context().Err() == nil {
		log.Printf("error streaming file %q: %v\n", path, copyErr)
	}
	return nil
}

func handleStreamLocalFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	no404 := r.URL.Query().Get("no404")
	handleLocalStreamFile(w, r, path, no404 != "")
}

func handleStreamFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	no404 := r.URL.Query().Get("no404")
	// path should already be formatted as a wsh:// URI (e.g. wsh://local/path or wsh://connection/path)
	err := handleStreamFileFromReader(w, r, path, no404 != "")
	if err != nil {
		log.Printf("error streaming file %q: %v\n", path, err)
		http.Error(w, fmt.Sprintf("error streaming file: %v", err), http.StatusInternalServerError)
	}
}

func WriteJsonError(w http.ResponseWriter, errVal error) {
	w.Header().Set(ContentTypeHeaderKey, ContentTypeJson)
	w.WriteHeader(http.StatusOK)
	errMap := make(map[string]interface{})
	errMap["error"] = errVal.Error()
	barr, _ := json.Marshal(errMap)
	w.Write(barr)
}

func WriteJsonSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set(ContentTypeHeaderKey, ContentTypeJson)
	rtnMap := make(map[string]interface{})
	rtnMap["success"] = true
	if data != nil {
		rtnMap["data"] = data
	}
	barr, err := json.Marshal(rtnMap)
	if err != nil {
		WriteJsonError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(barr)
}

type ClientActiveState struct {
	Fg     bool `json:"fg"`
	Active bool `json:"active"`
	Open   bool `json:"open"`
}

func WebFnWrap(opts WebFnOpts, fn WebFnType) WebFnType {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recErr := panichandler.PanicHandler("WebFnWrap", recover())
			if recErr == nil {
				return
			}
			if opts.JsonErrors {
				jsonRtn := marshalReturnValue(nil, recErr)
				w.Header().Set(ContentTypeHeaderKey, ContentTypeJson)
				w.Header().Set(ContentLengthHeaderKey, fmt.Sprintf("%d", len(jsonRtn)))
				w.WriteHeader(http.StatusOK)
				w.Write(jsonRtn)
			} else {
				http.Error(w, recErr.Error(), http.StatusInternalServerError)
			}
		}()
		if !opts.AllowCaching {
			w.Header().Set(CacheControlHeaderKey, CacheControlHeaderNoCache)
		}
		w.Header().Set("Access-Control-Expose-Headers", "X-ZoneFileInfo")

		// Handle CORS preflight OPTIONS requests without auth validation
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		err := authkey.ValidateIncomingRequest(r)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(fmt.Sprintf("error validating authkey: %v", err)))
			return
		}
		fn(w, r)
	}
}

func MakeTCPListener(serviceName string) (net.Listener, error) {
	serverAddr := "127.0.0.1:"
	rtn, err := net.Listen("tcp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("error creating listener at %v: %v", serverAddr, err)
	}
	log.Printf("Server [%s] listening on %s\n", serviceName, rtn.Addr())
	return rtn, nil
}

func MakeUnixListener() (net.Listener, error) {
	return MakeUnixListenerAt(wavebase.GetDomainSocketName(), "unix-domain")
}

// MakeUnixListenerAt creates a unix domain socket listener at an explicit path
// (0700, pre-existing socket file removed). Used for the web/ws servers when
// running in no-TCP mode (WAVETERM_WEB_UNIX_SOCKET).
func MakeUnixListenerAt(serverAddr string, serviceName string) (net.Listener, error) {
	os.Remove(serverAddr) // ignore error
	rtn, err := net.Listen("unix", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("error creating listener at %v: %v", serverAddr, err)
	}
	os.Chmod(serverAddr, 0700)
	log.Printf("Server [%s] listening on %s\n", serviceName, serverAddr)
	return rtn, nil
}

const schemaPrefix = "/schema/"

// blocking
func RunWebServer(listener net.Listener) {
	gr := mux.NewRouter()

	// Streaming routes must be registered before the /wave/ prefix catch-all to bypass TimeoutHandler.
	// http.TimeoutHandler buffers the entire response before flushing, which stalls streaming.
	gr.HandleFunc("/wave/stream-local-file", WebFnWrap(WebFnOpts{AllowCaching: true}, handleStreamLocalFile))
	gr.HandleFunc("/wave/stream-file", WebFnWrap(WebFnOpts{AllowCaching: true}, handleStreamFile))
	gr.PathPrefix("/wave/stream-file/").HandlerFunc(WebFnWrap(WebFnOpts{AllowCaching: true}, handleStreamFile))
	gr.HandleFunc("/api/post-chat-message", WebFnWrap(WebFnOpts{AllowCaching: false}, aiusechat.WaveAIPostMessageHandler))

	// Non-streaming /wave/ routes get timeout protection
	waveRouter := mux.NewRouter()
	waveRouter.HandleFunc("/wave/file", WebFnWrap(WebFnOpts{AllowCaching: false}, handleWaveFile))
	waveRouter.HandleFunc("/wave/service", WebFnWrap(WebFnOpts{JsonErrors: true}, handleService))
	waveRouter.HandleFunc("/wave/aichat", WebFnWrap(WebFnOpts{JsonErrors: true, AllowCaching: false}, aiusechat.WaveAIGetChatHandler))

	vdomRouter := mux.NewRouter()
	vdomRouter.HandleFunc("/vdom/{uuid}/{path:.*}", WebFnWrap(WebFnOpts{AllowCaching: true}, handleVDom))

	gr.PathPrefix("/wave/").Handler(http.TimeoutHandler(waveRouter, HttpTimeoutDuration, "Timeout"))
	gr.PathPrefix("/vdom/").Handler(http.TimeoutHandler(vdomRouter, HttpTimeoutDuration, "Timeout"))

	// Other routes without timeout
	gr.PathPrefix(schemaPrefix).Handler(http.StripPrefix(schemaPrefix, schema.GetSchemaHandler()))

	handler := http.Handler(gr)
	if wavebase.IsDevMode() {
		originalHandler := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Session-Id, X-AuthKey, Authorization, X-Requested-With, Accept, x-vercel-ai-ui-message-stream")
			w.Header().Set("Access-Control-Expose-Headers", "X-ZoneFileInfo, Content-Length, Content-Type, x-vercel-ai-ui-message-stream")
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			if r.Method == "OPTIONS" {
				w.WriteHeader(204)
				return
			}

			originalHandler.ServeHTTP(w, r)
		})
	}
	readTimeout := HttpReadTimeout
	if listener.Addr().Network() == "unix" {
		readTimeout = HttpUnixReadTimeout
	}
	server := &http.Server{
		ReadTimeout:    readTimeout,
		WriteTimeout:   HttpWriteTimeout,
		MaxHeaderBytes: HttpMaxHeaderBytes,
		Handler:        handler,
	}
	err := server.Serve(listener)
	if err != nil {
		log.Printf("ERROR: %v\n", err)
	}
}
