package handleAudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var AudioMessageCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "audio_messages_processed_total",
		Help: "Total number of processed audio messages.",
	},
	[]string{"status"}, // Status can be "success" or "error"
)

func AudioMessageHandle(bot *tgbotapi.BotAPI, message *tgbotapi.Message, endpoint string) {
	ctx, span := otel.Tracer("sr-tg-bot").Start(context.Background(), "handleAudioMessage")
	defer span.End()

	var fileID string
	var processStatus = "success" // Initially assume success, update to "error" as needed

	if message.Voice != nil {
		fileID = message.Voice.FileID
	} else if message.Audio != nil {
		fileID = message.Audio.FileID
	} else {
		log.Error().Msg("No audio or voice message found.")
		processStatus = "error"
		span.RecordError(errors.New("no audio or voice message found"))
		span.SetStatus(codes.Error, "No audio or voice message found")
		AudioMessageCounter.With(prometheus.Labels{"status": processStatus}).Inc()
		return
	}

	fileURL, err := bot.GetFileDirectURL(fileID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get file URL")
		processStatus = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to get file URL")
		AudioMessageCounter.With(prometheus.Labels{"status": processStatus}).Inc()
		return
	}

	// Download the audio file
	resp, err := http.Get(fileURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		log.Error().Err(err).Msg("Failed to download the audio file")
		processStatus = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to download the audio file")
		AudioMessageCounter.With(prometheus.Labels{"status": processStatus}).Inc()
		return
	}
	defer resp.Body.Close()

	// Create a temporary file to save the downloaded audio
	tempFile, err := os.CreateTemp("", "audio-*.ogg")
	if err != nil {
		log.Error().Err(err).Msg("Failed to create a temporary file")
		processStatus = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to create a temporary file")
		AudioMessageCounter.With(prometheus.Labels{"status": processStatus}).Inc()
		return
	}
	defer tempFile.Close()
	defer os.Remove(tempFile.Name()) // Ensure the temp file is removed after execution

	// Write the downloaded content to the temp file
	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		log.Error().Err(err).Msg("Failed to save the audio file to a temp file")
		processStatus = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to save the audio file to a temp file")
		AudioMessageCounter.With(prometheus.Labels{"status": processStatus}).Inc()
		return
	}

	// Prepare the request with the temp file for uploading
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_, err = tempFile.Seek(0, io.SeekStart) // Rewind the temp file to read from the beginning
	if err != nil {
		log.Error().Err(err).Msg("Failed to rewind temp file")
		processStatus = "error"
		span.RecordError(err)
		AudioMessageCounter.With(prometheus.Labels{"status": processStatus}).Inc()
		return
	}
	part, err := writer.CreateFormFile("file", "audio.ogg") // Adjusted form field name to "file"
	if err != nil {
		log.Error().Err(err).Msg("Failed to create form file for upload")
		processStatus = "error"
		span.RecordError(err)
		AudioMessageCounter.With(prometheus.Labels{"status": processStatus}).Inc()
		return
	}
	if _, err = io.Copy(part, tempFile); err != nil {
		log.Error().Err(err).Msg("Failed to copy temp file content to form file")
		processStatus = "error"
		span.RecordError(err)
		AudioMessageCounter.With(prometheus.Labels{"status": processStatus}).Inc()
		return
	}
	err = writer.Close()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to close writer")
		log.Error().Err(err).Msg("Failed to close writer")
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create a new request for uploading temp file")
		processStatus = "error"
		span.RecordError(err)
		return
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		log.Error().Err(err).Msg("Failed to upload the temp file")
		processStatus = "error"
		span.RecordError(err)
		return
	}
	defer resp.Body.Close()
	// Parse the response
	var recognition RecognitionSuccess
	if err := json.NewDecoder(resp.Body).Decode(&recognition); err != nil {
		log.Error().Err(err).Msg("Failed to decode recognition response")
		processStatus = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to decode recognition response")
		return
	}
	// Construct the response message
	responseMsg := fmt.Sprintf("Detected language: %s\nRecognized text: %s", recognition.DetectedLang, recognition.RecognizedText)

	// Send the response back to the user
	msg := tgbotapi.NewMessage(message.Chat.ID, responseMsg)
	if _, err := bot.Send(msg); err != nil {
		log.Error().Err(err).Msg("Failed to send recognition response to the Telegram user")
	}

	log.Info().Msg("Temporary audio file successfully uploaded")
	span.AddEvent("Temporary audio file uploaded", trace.WithAttributes(attribute.String("filename", tempFile.Name())))
	AudioMessageCounter.With(prometheus.Labels{"status": processStatus}).Inc()
}
