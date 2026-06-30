package audio

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/generate"
)

// fakeMP3 是一段假音频字节，测试只验证字节往返（hex 解码），不验真实音频。
var fakeMP3 = []byte{0x49, 0x44, 0x33, 0x04, 0x00, 0x01, 0x02, 0x03}

func TestMinimaxTTS_Generate(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody t2aRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		resp := t2aResponse{}
		resp.Data.Audio = hex.EncodeToString(fakeMP3)
		resp.BaseResp.StatusCode = 0
		resp.BaseResp.StatusMsg = "success"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := NewMinimaxTTS("sk-test", "speech-2.8-hd", srv.URL)
	res, err := g.Generate(context.Background(), generate.GenRequest{Prompt: "小兔子在森林里", Voice: "female-shaonv"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// 请求侧：打到 /t2a_v2，带 Bearer，body 透传 text/voice/model。
	if gotPath != "/t2a_v2" {
		t.Errorf("path = %q, want /t2a_v2", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q, want Bearer sk-test", gotAuth)
	}
	if gotBody.Text != "小兔子在森林里" {
		t.Errorf("text = %q", gotBody.Text)
	}
	if gotBody.VoiceSetting.VoiceID != "female-shaonv" {
		t.Errorf("voice_id = %q, want female-shaonv", gotBody.VoiceSetting.VoiceID)
	}
	if gotBody.Model != "speech-2.8-hd" {
		t.Errorf("model = %q", gotBody.Model)
	}
	if gotBody.AudioSetting.Format != "mp3" {
		t.Errorf("format = %q, want mp3", gotBody.AudioSetting.Format)
	}

	// 响应侧：hex 解码回原字节 + audio/mpeg + provider/model。
	if string(res.Bytes) != string(fakeMP3) {
		t.Errorf("bytes = %v, want %v", res.Bytes, fakeMP3)
	}
	if res.MimeType != "audio/mpeg" {
		t.Errorf("mime = %q, want audio/mpeg", res.MimeType)
	}
	if res.Provider != "minimax" || res.Model != "speech-2.8-hd" {
		t.Errorf("provider/model = %q/%q", res.Provider, res.Model)
	}
}

func TestMinimaxTTS_DefaultVoice(t *testing.T) {
	var gotVoice string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req t2aRequest
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		gotVoice = req.VoiceSetting.VoiceID
		resp := t2aResponse{}
		resp.Data.Audio = hex.EncodeToString(fakeMP3)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := NewMinimaxTTS("sk-test", "", srv.URL) // 空 model → 默认
	if _, err := g.Generate(context.Background(), generate.GenRequest{Prompt: "你好"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotVoice != defaultMinimaxVoice {
		t.Errorf("voice = %q, want default %q", gotVoice, defaultMinimaxVoice)
	}
}

func TestMinimaxTTS_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := t2aResponse{}
		resp.BaseResp.StatusCode = 1004
		resp.BaseResp.StatusMsg = "invalid api key"
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	g := NewMinimaxTTS("sk-bad", "speech-2.8-hd", srv.URL)
	_, err := g.Generate(context.Background(), generate.GenRequest{Prompt: "你好"})
	if err == nil || !strings.Contains(err.Error(), "1004") {
		t.Fatalf("want api error 1004, got %v", err)
	}
}

func TestMinimaxTTS_EmptyText(t *testing.T) {
	g := NewMinimaxTTS("sk-test", "speech-2.8-hd", "https://example.com/v1")
	if _, err := g.Generate(context.Background(), generate.GenRequest{Prompt: "  "}); err == nil {
		t.Fatal("want error on empty text")
	}
}

func TestMinimaxTTS_Kind(t *testing.T) {
	if k := NewMinimaxTTS("k", "m", "").Kind(); k != "audio" {
		t.Fatalf("Kind = %q, want audio", k)
	}
}
