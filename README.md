# imapsync-go

Синхронизация двух IMAP-аккаунтов на Go — аналог [imapsync](https://imapsync.lamiral.info) (Perl).

Односторонняя инкрементальная синхронизация: переносит сообщения из host1 в host2, пропуская уже существующие (по заголовкам `--useheader`), создаёт отсутствующие папки, сохраняет флаги (`\Seen`, `\Answered`, `\Flagged` и т. д.).

## Возможности

- **Соединения**: SSL/TLS (порт 993), STARTTLS (порт 143), plain; `--ssl-insecure` для самоподписанных сертификатов.
- **Аутентификация**: `LOGIN` (по умолчанию), `PLAIN` (с `--authuserN`), `XOAUTH2`, `OAUTHBEARER`; админ-аутентификация `--authuser1/2` и Cyrus `PROXYAUTH` (`--proxyauth1/2`).
- **Отбор папок**: `--folder`, `--folderrec`, `--subfolder1/2`, `--f1f2`, `--automap`, `--exclude1`, `--prefix1`, `--prefix2`.
- **Дедупликация**: по заголовкам `--useheader` (по умолчанию `Message-Id` + `Received`).
- **Флаги**: пересинхронизация флагов (`--noresyncflags` для отключения).
- **Фильтры**: `--minsize`, `--maxsize`, `--minage`, `--maxage`, `--skipmess` (regex по содержимому).
- **Ограничения**: `--maxmessagespersecond`, `--maxbytespersecond` (+ `--maxbytesafter`), `--errorsmax`, `--exitwhenover`.
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

## Примеры (проверено на сервере imap.example.com)

### SSL (порт 993) — полная синхронизация папки

```sh
./imapsync-go \
  --host1 imap.example.com --port1 993 --ssl1 \
  --user1 alice@example.com --password1 'secret1' \
  --host2 imap.example.com --port2 993 --ssl2 \
  --user2 bob@example.com --password2 'secret2' \
  --ssl-insecure \
  --folder Archives \
  --useheader Message-Id
```

Результат: 135 сообщений, 12 541 223 байт, 9 секунд, 0 ошибок, 1 папка создана.

### STARTTLS (порт 143)

```sh
./imapsync-go \
  --host1 imap.example.com --port1 143 --tls1 \
  --user1 alice@example.com --password1 'secret1' \
  --host2 imap.example.com --port2 143 --tls2 \
  --user2 bob@example.com --password2 'secret2' \
  --ssl-insecure \
  --justfoldersizes
```

### Dry-run (имитация)

```sh
./imapsync-go \
  --host1 imap.example.com --port1 993 --ssl1 \
  --user1 alice@example.com --password1 'secret1' \
  --host2 imap.example.com --port2 993 --ssl2 \
  --user2 bob@example.com --password2 'secret2' \
  --ssl-insecure \
  --dry --justfolders
```

### Параллельная синхронизация (2 потока)

```sh
./imapsync-go \
  --host1 imap.example.com --port1 993 --ssl1 \
  --user1 alice@example.com --password1 'secret1' \
  --host2 imap.example.com --port2 993 --ssl2 \
  --user2 bob@example.com --password2 'secret2' \
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

### Админ-аутентификация (--authuser1/--authuser2)

Логин под админ-учёткой с SASL `PLAIN` (authzid = `--authuserN`), доступ к чужому ящику. Как в imapsync, `--authuserN` по умолчанию включает `PLAIN` вместо `LOGIN`:

```sh
./imapsync-go \
  --host1 imap.example.com --user1 alice --password1 'adminpass' \
  --authuser1 admin --ssl1 \
  --host2 imap.example.com --user2 bob --password2 'adminpass' \
  --authuser2 admin --ssl2
```

### Cyrus PROXYAUTH (--proxyauth1/--proxyauth2)

Логин под админом через `LOGIN`, затем `PROXYAUTH <user>` (сервер Cyrus). Требует `--authuserN`:

```sh
./imapsync-go \
  --host1 imap.example.com --user1 alice --password1 'adminpass' \
  --authuser1 admin --proxyauth1 --ssl1 \
  --host2 imap.example.com --user2 bob --password2 'adminpass' \
  --authuser2 admin --proxyauth2 --ssl2
```

### Префиксы папок (--prefix1/--prefix2)

`--prefix1` удаляет префикс из имён папок источника, `--prefix2` добавляет префикс ко всем папкам назначения (кроме `INBOX`), как `prefix_seperator_invertion` в imapsync:

```sh
./imapsync-go \
  --host1 imap.src.ru --user1 a --password1 p1 --ssl1 \
  --host2 imap.dst.ru --user2 b --password2 p2 --ssl2 \
  --prefix1 "INBOX." --prefix2 "INBOX/"
```

`INBOX.Archive` с host1 попадёт в `INBOX/Archive` на host2; `INBOX` останется `INBOX`.

### Пропуск сообщений по regex (--skipmess)

Сообщения, полное содержимое которых совпадает с регулярным выражением, не переносятся (счётчик `skipped(regex)` в отчёте). Опция повторяется:

```sh
./imapsync-go \
  --host1 imap.src.ru --user1 a --password1 p1 --ssl1 \
  --host2 imap.dst.ru --user2 b --password2 p2 --ssl2 \
  --skipmess 'X-Spam-Flag: YES' --skipmess '^From:.*newsletter'
```

### Ограничение скорости (--maxmessagespersecond/--maxbytespersecond)

Как `sleep_if_needed` в imapsync: после каждого перенесённого сообщения при необходимости выполняется пауза (не более 2 с):

```sh
./imapsync-go \
  --host1 imap.src.ru --user1 a --password1 p1 --ssl1 \
  --host2 imap.dst.ru --user2 b --password2 p2 --ssl2 \
  --maxmessagespersecond 2 --maxbytespersecond 2000 --maxbytesafter 4000
```

`--maxbytesafter N` — первые N байт не учитываются при подсчёте байт-троттлинга.

### Остановка по числу ошибок (--errorsmax)

При достижении `N` ошибок синхронизация останавливается (по умолчанию 50, как `$ERRORS_MAX` в imapsync):

```sh
./imapsync-go ... --errorsmax 100
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
--authmech1/2            LOGIN (по умолчанию), PLAIN, XOAUTH2, OAUTHBEARER
--authuser1/2            пользователь для аутентификации (admin user);
                         по умолчанию включает PLAIN вместо LOGIN
--proxyauth1/2           выполнить PROXYAUTH после логина (требует --authuserN)
--timeout N              таймаут команды в секундах (0 = без таймаута)
--useheader H            заголовок для определения дубликатов (повторяется)
--skipheader RE          исключить заголовки, совпадающие с RE, из ключа идентичности
--sep1/--sep2            переопределить разделитель иерархии папок host1/host2
--search1/--search2      критерии IMAP SEARCH для фильтрации UID (UNSEEN, FLAGGED и т.д.)
--folder F               синхронизировать только папку F (повторяется)
--folderrec F            синхронизировать F и подпапки (повторяется)
--subfolder1/2           ограничить синхронизацию подпапкой
--f1f2 SRC DST           явное отображение папок (повторяется)
--automap                автоматически отображать одноимённые папки
--exclude1 P             исключить папки источника по подстроке
--include RE             включить только папки, совпадающие с regex RE (повторяется)
--exclude RE             исключить папки, совпадающие с regex RE (повторяется)
--folderfirst F          синхронизировать папку F в первую очередь (повторяется)
--folderlast F           синхронизировать папку F в последнюю очередь (повторяется)
--regextrans2 S          Perl-подобная замена имён папок s/from/to/flags (повторяется)
--prefix1 P              удалить префикс P из имён папок источника (напр. "INBOX.")
--prefix2 P              добавить префикс P ко всем папкам назначения (кроме INBOX)
--useuid                 использовать UID+кэш для распознавания сообщений (подразумевает --usecache)
--usecache               использовать локальный SQLite-кэш сопоставления UID
--cachedir DIR           каталог кэша (по умолчанию .imapsync_cache)
--delete1                удалить из источника после переноса (подразумевает --expunge1)
--delete2                удалить из назначения отсутствующие в источнике (подразумевает --uidexpunge2)
--delete2duplicates      удалить дубликаты внутри папок host2 (подразумевает --uidexpunge2)
--delete2folders         удалить папки назначения, отсутствующие в источнике
--delete1emptyfolders    удалить пустые папки источника
--subscribe2             подписаться на созданные папки назначения
--uidexpunge2            использовать UID EXPUNGE (RFC 4315) на host2
--expungeaftereach       expunge после каждого удаления (по умолчанию)
--noexpungeaftereach     отключить expunge после каждого удаления
--skipemptyfolders       пустые папки host1 не создаются на host2 (по умолчанию)
--noskipemptyfolders     создавать пустые папки host1 на host2
--skipcrossduplicates    не копировать сообщение, если ключ уже есть в любой папке host2
--noresyncflags          не пересинхронизировать флаги
--syncflagsaftercopy     синхронизировать флаги сразу после APPEND
--filterflags            фильтровать нестандартные системные флаги (по умолчанию)
--nofilterflags          отключить фильтрацию флагов
--regexflag S            Perl-подобная замена флагов s/from/to/ (повторяется)
--syncinternaldates      сохранять INTERNALDATE источника (по умолчанию)
--nosyncinternaldates    использовать текущую дату при APPEND
--minsize/--maxsize      фильтр по размеру сообщения (байт)
--minage/--maxage        фильтр по возрасту сообщения (дней)
--appendlimit N          пропускать сообщения больше N байт
--truncmess N            усекать сообщения до N байт
--skipmess RE            пропустить сообщения, содержимое которых совпадает с RE (повторяется)
--maxmessagespersecond N ограничить скорость: сообщений в секунду
--maxbytespersecond N    ограничить скорость: байт в секунду
--maxbytesafter N        байты, после которых учитывается --maxbytespersecond
--maxsleep N             максимальная пауза троттлинга в секундах (по умолчанию 2.0)
--errorsmax N            остановить при достижении N ошибок (по умолчанию 50)
--dry                    имитация без записи
--dry1                   dry-режим для host1 (по умолчанию при --dry)
--nodry1               отключить dry1
--logdir DIR             каталог для файлов журнала (по умолчанию LOG_imapsync)
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
├── main.go                      # CLI — тонкая обёртка над pkg/imapsync
├── pkg/
│   ├── imapsync/imapsync.go     # публичный фасад: Run/RunSync (точка входа для модуля)
│   ├── options/options.go       # разбор аргументов в стиле imapsync
│   ├── imapx/imapx.go           # обёртка go-imap: Connect, Login, Select, Fetch, Append
│   ├── sync/sync.go             # ядро: обход папок, дедупликация, перенос, флаги
│   ├── cache/cache.go           # SQLite-кэш для --usecache/--useuid
│   ├── logging/logging.go       # двойной вывод stdout + файл
│   └── report/report.go         # сбор статистики, итоговый отчёт
├── LOG_imapsync/                # журналы запусков (по умолчанию)
└── .imapsync_cache/             # SQLite-кэш (по умолчанию)
```

## Использование как модуля

Модуль: `github.com/Senimore/imapsync-go` (публичные пакеты в `pkg/`).

```sh
go get github.com/Senimore/imapsync-go
```

### Вариант 1 — полный цикл по аргументам (как CLI)

```go
package main

import (
	"os"

	"github.com/Senimore/imapsync-go/pkg/imapsync"
)

func main() {
	// Разбор аргументов, валидация, соединения, синхронизация, отчёт.
	os.Exit(imapsync.Run(os.Args[1:], os.Stdout))
}
```

### Вариант 2 — программный вызов с готовыми опциями

```go
package main

import (
	"os"

	"github.com/Senimore/imapsync-go/pkg/imapsync"
	"github.com/Senimore/imapsync-go/pkg/options"
)

func main() {
	opts, err := options.Parse([]string{
		"--host1", "imap.src.ru", "--user1", "a", "--password1", "p1",
		"--host2", "imap.dst.ru", "--user2", "b", "--password2", "p2",
		"--ssl1", "--ssl2", "--useheader", "Message-Id",
	})
	if err != nil {
		panic(err)
	}
	code := imapsync.RunSync(opts, os.Stdout)
	// Статистику можно получить через report.Snapshot (см. pkg/report).
	os.Exit(code)
}
```

### Коды выхода

| Константа | Значение | Смысл |
|-----------|----------|-------|
| `imapsync.ExOK` | 0 | успех |
| `imapsync.ExSoftware` | 100 | ошибка соединения/логина/синхронизации |
| `imapsync.ExCantCreat` | 73 | нельзя создать файл журнала |
| `imapsync.ExUsage` | 64 | ошибка разбора/валидации опций |

Низкоуровневые пакеты (`pkg/imapx`, `pkg/sync`, `pkg/cache`, `pkg/logging`, `pkg/report`) тоже публичные — их можно использовать напрямую для интеграций (например, `sync.New` + `Sync.Run` со своими соединениями и `report.Stats`).

Для приватных Gitea-репозиториев используйте `GOPRIVATE`/`GOPROXY=off` и `replace` в `go.mod` вашего проекта.

## Тестирование

```sh
go test -count=1 ./...
go test -race -count=1 ./...
```

Интеграционные тесты (`pkg/sync/integration_test.go`) используют in-memory IMAP-бэкенд.

## Примечания

- Папки с атрибутом `\Noselect` пропускаются (как `checkselectable` в imapsync).
- В режиме `--dry` отсутствие папки назначения не является ошибкой (она будет создана при реальном запуске).
- `--threads N` открывает по одному IMAP-соединению на горутину (у одного соединения активна одна папка).
- Синтаксис опций: `--key value` и `--key=value` (как в imapsync).
