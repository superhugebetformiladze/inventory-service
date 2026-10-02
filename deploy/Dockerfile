# Рабочий образ службы. Многоступенчатая сборка: в итоговый образ не попадают
# ни исходники, ни компилятор — только статические бинарники и корневые
# сертификаты.
#
# Три программы в одном образе, а не одна:
#
#   server    сама служба
#   migrate   накат миграций — отдельный шаг деплоя, до запуска службы
#   agentctl  токены раскатки, отзыв ключей, очередь разбора, выбытие машин
#
# Без двух последних рабочий сервер оказывается сервером, на котором нельзя
# ни обновить схему базы, ни выпустить токен для раскатки: в разработке эти
# команды идут через `go run`, а компилятора в рабочем образе нет.
FROM golang:1.27-alpine AS build

WORKDIR /src

# Зависимости отдельным слоем: они меняются реже кода, и пересобирать их на
# каждую правку — это минуты на ровном месте.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN for cmd in server migrate agentctl; do \
        CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/$cmd ./cmd/$cmd || exit 1; \
    done

# ---------------------------------------------------------------------------

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app

COPY --from=build /out/ /usr/local/bin/

USER app
EXPOSE 8080

# Не ENTRYPOINT, а CMD: одна и та же картинка запускается и службой, и
# разовой командой — `docker compose run --rm agentctl token …`.
CMD ["server"]
