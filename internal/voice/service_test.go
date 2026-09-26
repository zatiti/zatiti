package voice

import (
	"encoding/binary"
	"testing"
)

func testWAV(samples int, channels uint16, rate uint32) []byte {
	data := make([]byte, samples*2)
	b := make([]byte, 44+len(data))
	copy(b[:4], "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
	copy(b[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:20], 16)
	binary.LittleEndian.PutUint16(b[20:22], 1)
	binary.LittleEndian.PutUint16(b[22:24], channels)
	binary.LittleEndian.PutUint32(b[24:28], rate)
	binary.LittleEndian.PutUint32(b[28:32], rate*uint32(channels)*2)
	binary.LittleEndian.PutUint16(b[32:34], channels*2)
	binary.LittleEndian.PutUint16(b[34:36], 16)
	copy(b[36:40], "data")
	binary.LittleEndian.PutUint32(b[40:44], uint32(len(data)))
	copy(b[44:], data)
	return b
}

func TestValidWAVEnforcesNativeCaptureContractAndBounds(t *testing.T) {
	if got := testWAV(16000*15, 1, 16000); !validWAV(got) {
		t.Fatal("accepted native mono 16kHz PCM16 capture was rejected")
	}
	for name, wav := range map[string][]byte{
		"empty":        nil,
		"short header": testWAV(100, 1, 16000)[:43],
		"stereo":       testWAV(100, 2, 16000),
		"wrong rate":   testWAV(100, 1, 48000),
		"odd bytes":    append(testWAV(100, 1, 16000), 0),
		"too long":     testWAV(16000*15+1, 1, 16000),
	} {
		t.Run(name, func(t *testing.T) {
			if validWAV(wav) {
				t.Fatalf("accepted invalid WAV (%d bytes)", len(wav))
			}
		})
	}
	badLength := testWAV(100, 1, 16000)
	binary.LittleEndian.PutUint32(badLength[40:44], 98)
	if validWAV(badLength) {
		t.Fatal("accepted a WAV whose data size disagrees with its RIFF payload")
	}
}

func TestSpeechAdmissionEstimateUsesAffordableRoutesAndNarrateRewrite(t *testing.T) {
	base := Settings{Allowance: 100, Speech: "deepgram/flux-tts:free", Style: "conversational"}
	if cost, calls := estimate(base, "voice.transcribe", ""); cost != 100 || calls != 1 {
		t.Fatalf("transcription admission = (%d,%d), want (100,1)", cost, calls)
	}
	if cost, calls := estimate(base, "voice.speak", "Hello"); cost != 1000 || calls != 2 {
		t.Fatalf("free speech + Narrate craft admission = (%d,%d), want (1000,2)", cost, calls)
	}
	base.Speech = "hexgrad/kokoro-82m"
	if cost, calls := estimate(base, "voice.speak", "Hello"); cost != 1004 || calls != 2 {
		t.Fatalf("Kokoro plus Narrate craft admission = (%d,%d), want (1004,2)", cost, calls)
	}
	base.Style = "verbatim"
	if cost, calls := estimate(base, "voice.speak", "Hello"); cost != 100 || calls != 1 {
		t.Fatalf("verbatim Kokoro admission = (%d,%d), want (100,1)", cost, calls)
	}
}

func TestPersistentSpeechResultOmitsSynthesizedContent(t *testing.T) {
	out := result{
		Session: "session-id", Call: "call-id", Text: "private narration",
		Audio: "base64-secret-audio", Media: "audio/mpeg", Billing: "estimated",
		Reserved: 1000,
	}
	persisted := persistentSpeechResult(out)
	if persisted.Session != out.Session || persisted.Call != out.Call || persisted.Billing != out.Billing || persisted.Reserved != out.Reserved {
		t.Fatalf("persistent speech metadata lost: %+v", persisted)
	}
	if persisted.Text != "" || persisted.Audio != "" || persisted.Media != "" {
		t.Fatalf("persistent result retained speech content: %+v", persisted)
	}
}
