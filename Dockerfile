FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o temporal-reviewer .

FROM alpine:3.24

RUN apk --no-cache add ca-certificates wget

WORKDIR /app

COPY --from=builder /app/temporal-reviewer .
COPY --from=builder /app/config.yaml .
COPY --from=builder /app/dashboard/templates ./dashboard/templates
COPY --from=builder /app/dashboard/static ./dashboard/static
COPY --from=builder /app/prompts ./prompts

EXPOSE 8081 8082

CMD ["./temporal-reviewer"]
