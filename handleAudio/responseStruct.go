package handleAudio

type RecognitionSuccess struct {
	DetectedLang   string `json:"detected_language"`
	RecognizedText string `json:"recognized_text"`
}
