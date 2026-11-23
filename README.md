# fiapx-video-processor

Worker em Go que extrai os frames dos vídeos enviados à plataforma **FIAP X** (1 frame/s, PNG) e gera o `.zip`
para download. Consome `video.uploaded` no RabbitMQ, lê e grava no S3 e publica o resultado como evento.

> Parte da plataforma FIAP X: arquitetura, ADRs e contratos ficam em [`FiapXHacka/fiapx`](https://github.com/FiapXHacka/fiapx).

## Platform overview

```
video-api ── video.uploaded ──▶ RabbitMQ ──▶ video-processor (este repo, N réplicas × M goroutines)
                                                 │  S3: baixa uploads/…/original.*
                                                 │  ffprobe → ffmpeg -vf fps=1 → zip (streaming)
                                                 │  S3: grava results/…/frames.zip
video-api / notification ◀── processed | failed ─┘
```

## Origem

O primeiro commit deste repositório é o **protótipo original** apresentado aos investidores (Go + Gin, tudo em um
`main.go`, processamento dentro da requisição HTTP e arquivos em disco local). A lógica de extração foi mantida e
reorganizada em arquitetura hexagonal:

| Protótipo | Agora |
|---|---|
| `processVideo` (ffmpeg `fps=1`) | `internal/adapter/ffmpeg.Extractor`, com context, sem shell e com `-threads` configurável |
| `isValidVideoFile` (extensão) | `ffmpeg.Probe`: valida o **conteúdo** com ffprobe e mede a duração |
| `createZipFile` (zip em disco, deflate) | `internal/adapter/archive.Zip`: streaming para o S3, sem compressão (PNG já é comprimido) |
| upload, download e status via HTTP | foram para a `fiapx-video-api` (URLs pré-assinadas, ADR-0008) |
| `go run` no Dockerfile | build multi-stage, binário estático, usuário não-root |

## Estrutura

```
cmd/worker/            wiring: config, logs, health server, shutdown
internal/core/         caso de uso ProcessVideo + portas (Storage, VideoProbe, FrameExtractor, Archiver, EventPublisher)
internal/adapter/
  ffmpeg/              Extractor e Probe (binários ffmpeg/ffprobe)
  archive/             Zip em streaming
  s3/                  (issue #4)
  amqp/                consumer (#2) e publisher (#5)
internal/config/       variáveis de ambiente
internal/health/       /health/live e /health/ready
```

O núcleo decide o destino da mensagem:

| Retorno de `Process` | Significado | Ação do consumer |
|---|---|---|
| `nil` | resultado (`processed` ou `failed`) publicado e confirmado | ack |
| `ErrMalformedJob` | mensagem sem ids válidos, não há a quem reportar | ack + log |
| outro erro | transitório (S3, broker, cancelamento) | nack → reentrega/DLQ |

## Configuração

| Variável | Padrão | Descrição |
|---|---|---|
| `WORK_DIR` | `/work` | Diretório temporário (um subdiretório por vídeo, sempre removido) |
| `FRAMES_PER_SECOND` | `1` | Taxa de extração |
| `WORKER_CONCURRENCY` | `2` | Vídeos processados em paralelo por pod |
| `MAX_VIDEO_DURATION` | `10m` | Duração máxima (ADR-0018) |
| `MAX_VIDEO_SIZE_BYTES` | `524288000` | Tamanho máximo (500 MB) |
| `FFMPEG_THREADS` | `1` | Threads do ffmpeg por vídeo |
| `FFMPEG_TIMEOUT` | `15m` | Tempo máximo de extração |
| `HEALTH_ADDR` | `:8080` | Endereço do servidor de health |
| `SHUTDOWN_TIMEOUT` | `9m` | Tempo para terminar o vídeo atual no SIGTERM |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## Desenvolvimento

Requisitos: Go 1.27 (versão fixada em `.tool-versions`: `asdf install` instala a versão certa) e ffmpeg
(`brew install ffmpeg`; sem ele, os testes de integração do adaptador são pulados).

```bash
go test ./... -race -cover
golangci-lint run ./...
docker build -t fiapx-video-processor .
```

Sem Go instalado localmente:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27-alpine \
  sh -c 'apk add -q ffmpeg build-base && CGO_ENABLED=1 go test ./... -race -cover'
```

## CI

`.github/workflows/ci.yml`: vet → golangci-lint → testes com `-race` e cobertura (ffmpeg real) → SonarCloud → build da imagem.
A análise no SonarQube Cloud é *secretless*: o job troca seu token OIDC do GitHub por um token de 30 minutos
(`sonarqube-auth-action`), sem `SONAR_TOKEN` armazenado. Requer o repositório vinculado ao projeto
`FiapXHacka_fiapx-video-processor` no SonarQube Cloud.
