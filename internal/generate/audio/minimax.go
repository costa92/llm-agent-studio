// Package audio houses real audio (TTS) generation adapters. The M4 key-gated
// skeletons (OpenAI tts-1 not-configured stub) were retired in Phase 2.1 —
// only wired, working adapters live here (currently MiniMax T2A).
package audio

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/costa92/llm-agent-studio/internal/generate"
)

// defaultMinimaxVoice 是工作流节点未透传音色（GenRequest.Voice 为空）时的
// 兜底 voice_id。MiniMax 的预置中文音色之一，温和，适合旁白朗读。
const defaultMinimaxVoice = "male-qn-qingse"

const (
	minimaxDefaultBaseURL = "https://api.minimaxi.com/v1"
	minimaxDefaultModel   = "speech-2.8-hd"
	minimaxMaxRespBytes   = 32 << 20 // 单条 TTS 响应上限（hex 文本，远超任何单页旁白）
)

// minimaxTTS 调 MiniMax T2A v2（text-to-audio）接口，把一段旁白文本同步转成 MP3
// 字节。实现 generate.MediaGenerator（同步 Generate）——T2A 是同步接口（~1s 返回
// data.audio 的 hex），故 NOT 实现 AsyncGenerator：worker 的 routed.(AsyncGenerator)
// 断言为假 → 走与 image 相同的同步单遍路径（submit/poll 异步引擎不介入）。
//
// 这是本包唯一的真实 TTS 实现（M4 的 OpenAI 桩骨架已随 Phase 2.1 下架）。
type minimaxTTS struct {
	apiKey  string
	model   string
	baseURL string
	hc      *http.Client
}

// NewMinimaxTTS 构造一个 MiniMax T2A 生成器。model 取 org model_config 的 model
// （如 speech-2.8-hd），baseURL 取其 base_url（如 https://api.minimaxi.com/v1）；
// 二者为空时回落到 MiniMax 国际版默认。
func NewMinimaxTTS(apiKey, model, baseURL string) *minimaxTTS {
	if model == "" {
		model = minimaxDefaultModel
	}
	if baseURL == "" {
		baseURL = minimaxDefaultBaseURL
	}
	return &minimaxTTS{
		apiKey:  apiKey,
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		hc:      &http.Client{Timeout: 60 * time.Second},
	}
}

func (m *minimaxTTS) Kind() string { return "audio" }

type t2aVoiceSetting struct {
	VoiceID string  `json:"voice_id"`
	Speed   float64 `json:"speed"`
	Vol     float64 `json:"vol"`
	Pitch   int     `json:"pitch"`
}

type t2aAudioSetting struct {
	SampleRate int    `json:"sample_rate"`
	Bitrate    int    `json:"bitrate"`
	Format     string `json:"format"`
	Channel    int    `json:"channel"`
}

type t2aRequest struct {
	Model        string          `json:"model"`
	Text         string          `json:"text"`
	Stream       bool            `json:"stream"`
	VoiceSetting t2aVoiceSetting `json:"voice_setting"`
	AudioSetting t2aAudioSetting `json:"audio_setting"`
}

type t2aResponse struct {
	Data struct {
		Audio  string `json:"audio"` // hex 编码的 MP3 字节
		Status int    `json:"status"`
	} `json:"data"`
	BaseResp struct {
		StatusCode int    `json:"status_code"` // 0 = 成功
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

// Generate 把 req.Prompt（旁白文本）按 req.Voice（音色，空则默认）合成为 MP3。
// 返回 GenResult{Bytes: mp3, MimeType: "audio/mpeg", Provider/Model}——worker 同步
// 路径据此把字节落 blob（mimeToExt(audio/mpeg)=.mp3）、把 provider/model 写入资产。
func (m *minimaxTTS) Generate(ctx context.Context, req generate.GenRequest) (generate.GenResult, error) {
	text := strings.TrimSpace(req.Prompt)
	if text == "" {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: empty text")
	}
	voice := req.Voice
	if voice == "" {
		voice = defaultMinimaxVoice
	}

	body, err := json.Marshal(t2aRequest{
		Model:  m.model,
		Text:   text,
		Stream: false,
		VoiceSetting: t2aVoiceSetting{
			VoiceID: voice, Speed: 1.0, Vol: 1.0, Pitch: 0,
		},
		AudioSetting: t2aAudioSetting{
			SampleRate: 32000, Bitrate: 128000, Format: "mp3", Channel: 1,
		},
	})
	if err != nil {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/t2a_v2", bytes.NewReader(body))
	if err != nil {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.hc.Do(httpReq)
	if err != nil {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: do: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, minimaxMaxRespBytes))
	if err != nil {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: http %d: %s", resp.StatusCode, snippet(raw))
	}

	var tr t2aResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: decode: %w", err)
	}
	if tr.BaseResp.StatusCode != 0 {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: api error %d: %s",
			tr.BaseResp.StatusCode, tr.BaseResp.StatusMsg)
	}
	audioBytes, err := hex.DecodeString(tr.Data.Audio)
	if err != nil {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: hex decode: %w", err)
	}
	if len(audioBytes) == 0 {
		return generate.GenResult{}, fmt.Errorf("generate.audio: minimax tts: empty audio")
	}

	return generate.GenResult{
		Bytes:    audioBytes,
		MimeType: "audio/mpeg",
		Provider: "minimax",
		Model:    m.model,
	}, nil
}

// snippet 截断响应体用于错误信息，避免把整段 hex 灌进日志。
func snippet(b []byte) string {
	const max = 200
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
