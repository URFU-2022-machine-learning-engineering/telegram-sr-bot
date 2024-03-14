package main

import (
	"context"
	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"net/http"
	"os"
	"telegram-sr-bot/handleAudio"
)

func init() {
	prometheus.MustRegister(handleAudio.AudioMessageCounter)
	// Set up zerolog
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}

func main() {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal().Msg("TELEGRAM_BOT_TOKEN environment variable is not set")
	}
	endpoint := os.Getenv("API_ENDPOINT")
	if endpoint == "" {
		log.Warn().Msg("API_ENDPOINT environment variable is " +
			"not set, using default value: \"http://127.0.0.1:8787/upload\"")
		endpoint = "http://127.0.0.1:8787/upload"
	}
	log.Debug().Msgf("Endpoint is %s", endpoint)

	http.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(":2112", nil); err != nil {
			log.Fatal().Err(err).Msg("Failed to start metrics server")
		}
	}()

	// Set up OpenTelemetry
	tp := initTracing()
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Fatal().Err(err).Msg("Failed to shut down trace provider")
		}
	}()

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create bot")
	}

	bot.Debug = true

	log.Info().Msgf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil && (update.Message.Voice != nil || update.Message.Audio != nil) {
			log.Info().Msg("Audio or voice message received")
			_, span := otel.Tracer("telegram-sr-bot").Start(context.Background(), "processMessage")
			span.SetAttributes(attribute.String("type", "audioMessage"))

			handleAudio.AudioMessageHandle(bot, update.Message, endpoint)
			span.SetStatus(codes.Ok, "Processing succeeded")
			span.End()
		}
	}
}

func initTracing() *sdktrace.TracerProvider {
	ctx := context.Background()
	otelCollectorEndpoint := os.Getenv("TELEMETRY_GRPC_TARGET")
	if otelCollectorEndpoint == "" {
		log.Fatal().Msg("TELEMETRY_GRPC_TARGET environment variable is not set")
	}

	// Initialize the OTLP exporter to send trace data to an OTel Collector over gRPC
	exporter, err := otlptrace.New(ctx, otlptracegrpc.NewClient(
		otlptracegrpc.WithEndpoint(otelCollectorEndpoint),
		otlptracegrpc.WithInsecure(),
	))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create OTLP trace exporter")
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", "telegram-sr-bot"),
	))
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create resource")
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	return tp
}
