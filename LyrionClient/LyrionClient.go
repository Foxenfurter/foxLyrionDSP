package LyrionClient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	host       string
	port       int
	httpClient *http.Client
}

func New(host string, port int) *Client {
	return &Client{
		host: host,
		port: port,
		httpClient: &http.Client{
			Timeout: 500 * time.Millisecond,
		},
	}
}

func (c *Client) endpoint() string {
	return fmt.Sprintf("http://%s:%d/jsonrpc.js", c.host, c.port)
}

type jsonRPCRequest struct {
	ID     int           `json:"id"`
	Method string        `json:"method"`
	Params []interface{} `json:"params"`
}

type jsonRPCResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  interface{}     `json:"error"`
}

func (c *Client) Request(ctx context.Context, playerID string, params []interface{}) (json.RawMessage, error) {
	payload := jsonRPCRequest{
		ID:     1,
		Method: "slim.request",
		Params: []interface{}{playerID, params},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	request := fmt.Sprintf(
		"POST /jsonrpc.js HTTP/1.0\r\n"+
			"Host: %s:%d\r\n"+
			"Content-Type: application/json\r\n"+
			"Content-Length: %d\r\n"+
			"Connection: close\r\n"+
			"\r\n%s",
		c.host, c.port, len(body), string(body),
	)

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", c.host, c.port))
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	if _, err := conn.Write([]byte(request)); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	buf := make([]byte, 131072) // doubled to 128KB
	n, err := readAll(conn, buf)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	response := string(buf[:n])

	// try \r\n\r\n first, fall back to \n\n
	separator := "\r\n\r\n"
	parts := strings.SplitN(response, separator, 2)
	if len(parts) != 2 {
		separator = "\n\n"
		parts = strings.SplitN(response, separator, 2)
	}
	if len(parts) != 2 {
		// log first 500 chars of raw response for diagnosis
		preview := response
		if len(preview) > 500 {
			preview = preview[:500]
		}
		return nil, fmt.Errorf("malformed response, raw: %s", preview)
	}

	responseBody := parts[1]

	// handle chunked transfer encoding
	headers := strings.ToLower(parts[0])
	if strings.Contains(headers, "transfer-encoding: chunked") {
		responseBody, err = decodeChunked(responseBody)
		if err != nil {
			return nil, fmt.Errorf("decode chunked: %w", err)
		}
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal([]byte(responseBody), &rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error: %v", rpcResp.Error)
	}

	return rpcResp.Result, nil
}

// decodeChunked strips HTTP chunked transfer encoding markers
func decodeChunked(s string) (string, error) {
	var result strings.Builder
	for len(s) > 0 {
		// find chunk size line
		idx := strings.Index(s, "\r\n")
		if idx < 0 {
			break
		}
		sizeStr := strings.TrimSpace(s[:idx])
		if sizeStr == "" {
			break
		}
		var size int
		if _, err := fmt.Sscanf(sizeStr, "%x", &size); err != nil {
			return "", fmt.Errorf("invalid chunk size %q: %w", sizeStr, err)
		}
		if size == 0 {
			break
		}
		s = s[idx+2:]
		if len(s) < size {
			return "", fmt.Errorf("chunk truncated")
		}
		result.WriteString(s[:size])
		s = s[size:]
		// skip trailing \r\n after chunk data
		if strings.HasPrefix(s, "\r\n") {
			s = s[2:]
		}
	}
	return result.String(), nil
}

func readAll(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			// EOF is expected on HTTP/1.0 close
			if err.Error() == "EOF" || strings.Contains(err.Error(), "use of closed") {
				return total, nil
			}
			return total, err
		}
	}
}

type trackGainResponse struct {
	URL           string      `json:"url"`
	TrackGain     interface{} `json:"track_gain"`
	TrackPeak     interface{} `json:"track_peak"`
	AlbumGain     interface{} `json:"album_gain"`
	AlbumPeak     interface{} `json:"album_peak"`
	AlbumMatch    int         `json:"album_match"`
	AlbumID       string      `json:"album_id"` // *** NEW ***
	NextURL       string      `json:"next_url"`
	NextTrackGain interface{} `json:"next_track_gain"`
	NextTrackPeak interface{} `json:"next_track_peak"`
	NextAlbumGain interface{} `json:"next_album_gain"`
	NextAlbumPeak interface{} `json:"next_album_peak"`
	NextAlbumID   string      `json:"next_album_id"` // *** NEW ***
	Error         string      `json:"error"`
}

type TrackGainResult struct {
	URL           string
	TrackGain     *float64
	TrackPeak     *float64
	AlbumGain     *float64
	AlbumPeak     *float64
	AlbumMatch    bool
	AlbumID       string // *** NEW ***
	NextURL       string
	NextTrackGain *float64
	NextTrackPeak *float64
	NextAlbumGain *float64
	NextAlbumPeak *float64
	NextAlbumID   string // *** NEW ***
}

func parseOptionalFloat(n json.Number) *float64 {
	s := n.String()
	if s == "" || s == "null" || s == "0" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}

// parseOptionalFloatInterface safely converts an interface{} to *float64 using json.Number or string
func parseOptionalFloatInterface(val interface{}) *float64 {
	switch v := val.(type) {
	case json.Number:
		return parseOptionalFloat(v)
	case string:
		if v == "" || v == "null" || v == "0" {
			return nil
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil
		}
		return &f
	case float64:
		return &v
	case float32:
		f := float64(v)
		return &f
	case int:
		f := float64(v)
		return &f
	case int64:
		f := float64(v)
		return &f
	default:
		return nil
	}
}

func (c *Client) GetCurrentTrackGain(ctx context.Context, playerID string) (*TrackGainResult, error) {
	raw, err := c.Request(ctx, playerID, []interface{}{"squeezedsp.trackgain"})
	if err != nil {
		return nil, fmt.Errorf("trackgain query: %w", err)
	}

	var resp trackGainResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal trackgain: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("trackgain error: %s", resp.Error)
	}

	return &TrackGainResult{
		URL:           resp.URL,
		TrackGain:     parseOptionalFloatInterface(resp.TrackGain),
		TrackPeak:     parseOptionalFloatInterface(resp.TrackPeak),
		AlbumGain:     parseOptionalFloatInterface(resp.AlbumGain),
		AlbumPeak:     parseOptionalFloatInterface(resp.AlbumPeak),
		AlbumMatch:    resp.AlbumMatch == 1,
		AlbumID:       resp.AlbumID, // *** NEW ***
		NextURL:       resp.NextURL,
		NextTrackGain: parseOptionalFloatInterface(resp.NextTrackGain),
		NextTrackPeak: parseOptionalFloatInterface(resp.NextTrackPeak),
		NextAlbumGain: parseOptionalFloatInterface(resp.NextAlbumGain),
		NextAlbumPeak: parseOptionalFloatInterface(resp.NextAlbumPeak),
		NextAlbumID:   resp.NextAlbumID, // *** NEW ***
	}, nil
}
