# imapsync-go

Синхронизация двух IMAP-аккаунтов на Go — аналог [imapsync](https://imapsync.lamiral.info) (Perl).

Односторонняя инкрементальная синхронизация: переносит сообщения из host1 в host2, пропуская уже существующие (по заголовкам `--useheader`), создаёт отсутствующие папки, сохраняет флаги (`\Seen`, `\Answered`, `\Flagged` и т. д.).

## Возможности

- **Соединения**: SSL/TLS (порт 993), STARTTLS (порт 143), plain; `--ssl-insecure` для самоподписанных сертификатов.
- **Аутентификация**: `LOGIN` (по умолчанию), `XOAUTH2`, `OAUTHBEARER`.
- **Отбор папок**: `--folder`, `--folderrec`, `--subfolder1/2`, `--f1f2`, `--automap`, `--exclude1`.
- **Дедупликация**: по заголовкам `--useheader` (по умолчанию `Message-Id` + `Received`).
- **Флаги**: пересинхронизация флагов (`--noresyncflags` для отключения).
- **Фильтры**: `--minsize`, `--maxsize`, `--minage`, `--maxage`.
- **Удаления**: `--delete1`, `--delete2`, `--delete2folders`, `--delete1emptyfolders`, `--expunge1`, `--expunge2`.
- **Пустые папки**: `--skipemptyfolders` (по умолчанию) / `--noskipemptyfolders`.
- **Параллелизм**: `--threads N` (каждая горутина получает свою пару соединений).
- **Режимы**: `--dry`, `--justconnect`, `--justlogin`, `--justfolders`, `--justfoldersizes`, `--justbanner`.
- **Логирование**: двойной вывод (stdout + файл), `--logfile`, `--nolog`, `--debug`, `--debugimap`.
- **Отчёт**: imapsync-совместимый итоговый отчёт и коды выхода.

## Сборка

```sh
cd imapsync-go
go build -o imapsync-go .
```

Требования: Go 1.21+.

Зависимости:
- `github.com/emersion/go-imap` — IMAP-клиент
- `github.com/emersion/go-sasl` — SASL-механизмы (XOAUTH2, OAUTHBEARER)

## Быстрый старт

```sh
./imapsync-go \
  --host1 imap.example.com --user1 alice --password1 'secret1' \
  --host2 imap.example.com --user2 bob   --password2 'secret2' \
  --ssl1 --ssl2 --useheader Message-Id
```

## Соединение: SSL и STARTTLS

| Порт | Режим | Флаги |
|------|-------|-------|
| 993  | SSL/TLS (imaps) | `--ssl1 --ssl2` |
| 143  | STARTTLS | `--tls1 --tls2` |
| 143  | plain (без шифрования) | без флагов |

Порт по умолчанию: **993** при `--sslN`, иначе **143**. Явный порт: `--port1 993`.

Для самоподписанных сертификатов: `--ssl-insecure`.

## Примеры (проверено на сервере 10.0.30.34)

### SSL (порт 993) — полная синхронизация папки

```sh
./imapsync-go \
  --host1 10.0.30.34 --port1 993 --ssl1 \
  --user1 test@exmail.ru --password1 '100%temp' \
  --host2 10.0.30.34 --port2 993 --ssl2 \
  --user2 test1@exmail.ru --password2 '100%temp' \
  --ssl-insecure \
  --folder Archives \
  --useheader Message-Id
```

Результат: 135 сообщений, 12 541 223 байт, 9 секунд, 0 ошибок, 1 папка создана.

### STARTTLS (порт 143)

```sh
./imapsync-go \
  --host1 10.0.30.34 --port1 143 --tls1 \
  --user1 test@exmail.ru --password1 '100%temp' \
  --host2 10.0.30.34 --port2 143 --tls2 \
  --user2 test1@exmail.ru --password2 '100%temp' \
  --ssl-insecure \
  --justfoldersizes
```

### Dry-run (имитация)

```sh
./imapsync-go \
  --host1 10.0.30.34 --port1 993 --ssl1 \
  --user1 test@exmail.ru --password1 '100%temp' \
  --host2 10.0.30.34 --port2 993 --ssl2 \
  --user2 test1@exmail.ru --password2 '100%temp' \
  --ssl-insecure \
  --dry --justfolders
```

### Параллельная синхронизация (2 потока)

```sh
./imapsync-go \
  --host1 10.0.30.34 --port1 993 --ssl1 \
  --user1 test@exmail.ru --password1 '100%temp' \
  --host2 10.0.30.34 --port2 993 --ssl2 \
  --user2 test1@exmail.ru --password2 '100%temp' \
  --ssl-insecure \
  --threads 2
```

### Отображение папок (--f1f2)

```sh
./imapsync-go \
  --host1 imap.src.ru --user1 a --password1 p1 --ssl1 \
  --host2 imap.dst.ru --user2 b --password2 p2 --ssl2 \
  --f1f2 "INBOX" "INBOX" \
  --f1f2 "Sent Items" "Sent" \
  --f1f2 "Deleted Items" "Trash"
```

### Автоматическое отображение одноимённых папок

```sh
./imapsync-go \
  --host1 imap.src.ru --user1 a --password1 p1 --ssl1 \
  --host2 imap.dst.ru --user2 b --password2 p2 --ssl2 \
  --automap
```

### Синхронизация папки и подпапок

```sh
./imapsync-go \
  --host1 imap.src.ru --user1 a --password1 p1 --ssl1 \
  --host2 imap.dst.ru --user2 b --password2 p2 --ssl2 \
  --folderrec "Archive"
```

### Фильтры по размеру и возрасту

```sh
./imapsync-go \
  --host1 imap.src.ru --user1 a --password1 p1 --ssl1 \
  --host2 imap.dst.ru --user2 b --password2 p2 --ssl2 \
  --minsize 1024 \
  --maxsize 10485760 \
  --maxage 365
```

### Синхронизация с удалением из источника

```sh
./imapsync-go \
  --host1 imap.src.ru --user1 a --password1 p1 --ssl1 \
  --host2 imap.dst.ru --user2 b --password2 p2 --ssl2 \
  --delete1
```

`--delete1` подразумевает `--expunge1` (как в imapsync).

### XOAUTH2 (OAuth2-токен)

```sh
./imapsync-go \
  --host1 imap.gmail.com --user1 user@gmail.com --password1 'ya29.xxxx' \
  --authmech1 XOAUTH2 --ssl1 \
  --host2 imap.example.com --user2 b --password2 p2 --ssl2
```

## Справочник опций

```
--host1/--host2          адрес IMAP-сервера источника/назначения
--user1/--user2          имя пользователя
--password1/--password2  пароль (для XOAUTH2 — access token)
--port1/--port2          порт (по умолчанию 993 при --sslN, иначе 143)
--ssl1/--ssl2            imap over TLS (imaps)
--tls1/--tls2            STARTTLS
--ssl-insecure           не проверять TLS-сертификат
--authmech1/2            LOGIN (по умолчанию) или XOAUTH2
--timeout N              таймаут команды в секундах (0 = без таймаута)
--useheader H            заголовок для определения дубликатов (повторяется)
--folder F               синхронизировать только папку F (повторяется)
--folderrec F            синхронизировать F и подпапки (повторяется)
--subfolder1/2           ограничить синхронизацию подпапкой
--f1f2 SRC DST           явное отображение папок (повторяется)
--automap                автоматически отображать одноимённые папки
--exclude1 P             исключить папки источника по подстроке
--delete1                удалить из источника после переноса (подразумевает --expunge1)
--delete2                удалить из назначения отсутствующие в источнике
--delete2folders         удалить папки назначения, отсутствующие в источнике
--delete1emptyfolders    удалить пустые папки источника
--subscribe2             подписаться на созданные папки назначения
--skipemptyfolders       пустые папки host1 не создаются на host2 (по умолчанию)
--noskipemptyfolders     создавать пустые папки host1 на host2
--noresyncflags          не пересинхронизировать флаги
--minsize/--maxsize      фильтр по размеру сообщения (байт)
--minage/--maxage        фильтр по возрасту сообщения (дней)
--dry                    имитация без записи
--justconnect            только подключиться и показать capabilities
--justlogin              только подключиться и залогиниться
--justfolders            только список папок источника
--justfoldersizes        только размеры папок источника
--logfile FILE           файл журнала (по умолчанию LOG_imapsync/<stamp>_<u1>_<u2>.txt)
--nolog                  не писать журнал
--debug                  подробный вывод
--debugimap              IMAP-протокол в stdout
--threads N              число параллельных потоков синхронизации папок
--exitwhenover N         остановить после переноса N байт
--addheader              добавить заголовок X-IMAPSYNC при переносе
--version                показать версию
--help                   справка
```

## Коды выхода

| Код | Имя | Значение |
|-----|-----|----------|
| 0   | EX_OK | успешное завершение |
| 64  | EX_USAGE | ошибка аргументов командной строки |
| 73  | EX_CANTCREAT | не удалось создать файл журнала |
| 100 | EX_SOFTWARE | ошибка выполнения (соединение, логин, синхронизация) |

## Идемпотентность

Повторный запуск с теми же параметрами не переносит сообщения повторно: они определяются как дубликаты по заголовку `Message-Id` и пропускаются. Пример повторного запуска после синхронизации 135 сообщений:

```
Синхронизация папки "Archives" -> "Archives"
  135 дубликатов пропущено
  0 сообщений перенесено
```

## Структура проекта

```
imapsync-go/
├── main.go                      # CLI, подключение, спец-режимы, коды выхода
├── internal/
│   ├── options/options.go       # разбор аргументов в стиле imapsync
│   ├── imapx/imapx.go           # обёртка go-imap: Connect, Login, Select, Fetch, Append
│   ├── sync/sync.go             # ядро: обход папок, дедупликация, перенос, флаги
│   ├── logging/logging.go       # двойной вывод stdout + файл
│   └── report/report.go         # сбор статистики, итоговый отчёт
└── LOG_imapsync/                # журналы запусков (по умолчанию)
```

## Тестирование

```sh
go test -count=1 ./...
go test -race -count=1 ./...
```

Интеграционные тесты (`internal/sync/integration_test.go`) используют in-memory IMAP-бэкенд.

## Примечания

- Папки с атрибутом `\Noselect` пропускаются (как `checkselectable` в imapsync).
- В режиме `--dry` отсутствие папки назначения не является ошибкой (она будет создана при реальном запуске).
- `--threads N` открывает по одному IMAP-соединению на горутину (у одного соединения активна одна папка).
- Синтаксис опций: `--key value` и `--key=value` (как в imapsync).
