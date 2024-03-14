# Build stage
FROM golang:1.21 AS build

WORKDIR /app

# Copy go mod and sum files first to leverage Docker cache
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application source code
COPY . .

# Compile the application to a static binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /telegram-sr-bot

# Final stage, using a distroless image for minimal footprint
FROM gcr.io/distroless/static

# Copy the static binary from the build stage
COPY --from=build /telegram-sr-bot /telegram-sr-bot

# Specify the user to run the application (non-root for security)
USER nonroot:nonroot

EXPOSE 8080

CMD ["/telegram-sr-bot"]
