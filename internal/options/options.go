// Package options реализует разбор аргументов командной строки в стиле
// оригинального Perl-скрипта imapsync.
//
// Поддерживается синтаксис: --key value, --key=value, булевы --key,
// а также многократные опции (--useheader, --folder, --folderrec, --f1f2).
package options

import (
	"fmt"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Version — версия Go-реализации.
const Version = "0.1.0"

// Options хранит все настройки запуска, сопоставимые с imapsync.
type Options struct {
	// Учётные записи.
	Host1, User1, Password1 string
	Host2, User2, Password2 string
	Port1, Port2            int

	// Механизмы аутентификации: LOGIN (по умолчанию) или XOAUTH2.
	AuthMech1, AuthMech2 string

	// Административная (proxy) аутентификация, как в imapsync:
	// --authuser1/--authuser2 — пользователь, под которым выполняется
	// аутентификация (admin user); --proxyauth1/--proxyauth2 — команда
	// PROXYAUTH после логина (требует --authuserN).
	AuthUser1, AuthUser2   string
	ProxyAuth1, ProxyAuth2 bool

	// TLS.
	SSL1, SSL2 bool // imap over TLS (imaps), порт 993 по умолчанию
	TLS1, TLS2 bool // STARTTLS поверх обычного соединения
	Insecure   bool // --ssl-insecure: не проверять сертификат
	TimeoutSec int  // таймаут команды в секундах (0 = без таймаута)
	Compress1  bool
	Compress2  bool

	// Идентификация сообщений.
	UseHeader []string // заголовки для определения дубликатов

	// Отбор папок.
	Folder     []string // синхронизировать только эти папки (точное имя)
	FolderRec  []string // синхронизировать папку и все её подпапки
	Subfolder1 string   // префикс-подпапка источника
	Subfolder2 string   // префикс-подпапка назначения
	F1F2       [][2]string
	Automap    bool
	Exclude1   []string // исключить папки источника (по подстроке)
	ExcludeAll []string

	// Префиксы папок (как в imapsync): --prefix1 — префикс, который
	// удаляется из имён папок источника (обычно "INBOX." или "INBOX/"),
	// --prefix2 — префикс, который добавляется ко всем папкам host2.
	Prefix1, Prefix2 string

	// Удаления.
	Delete1             bool // удалить из источника после переноса
	Expunge1            bool
	Delete2             bool // удалить из назначения то, чего нет в источнике
	Expunge2            bool
	Delete2Folders      bool // удалить папки назначения, отсутствующие в источнике
	Delete1EmptyFolders bool
	Subscribe2          bool // подписаться на созданные папки назначения

	// Пустые папки (как в imapsync: по умолчанию skipemptyfolders = 1).
	SkipEmptyFolders bool

	// Флаги.
	NoResyncFlags bool // не пересинхронизировать флаги

	// Фильтры сообщений.
	MinSize, MaxSize int64
	MinAge, MaxAge   float64 // в днях
	Search           string  // доп. критерий (не реализован полностью)

	// --skipmess: список регулярных выражений; сообщение пропускается,
	// если его полное содержимое совпадает с любым из них (как в imapsync).
	SkipMess []string
	// SkipMessRe — скомпилированные SkipMess (заполняется в Parse).
	SkipMessRe []*regexp.Regexp

	// Ограничение скорости (как в imapsync):
	// --maxmessagespersecond — максимум перенесённых сообщений в секунду;
	// --maxbytespersecond — максимум перенесённых байт в секунду;
	// --maxbytesafter — байты, после которых начинается учёт для
	// --maxbytespersecond (до этого порога байты не учитываются).
	MaxMessagesPerSecond float64
	MaxBytesPerSecond    int64
	MaxBytesAfter        int64

	// --errorsmax: максимальное число ошибок, при котором imapsync
	// останавливается (по умолчанию 50, как в оригинале).
	ErrorsMax int

	// Режимы.
	Dry             bool
	JustConnect     bool
	JustLogin       bool
	JustFolders     bool
	JustFoldersizes bool
	JustBanner      bool

	// Прочее.
	Logfile      string
	NoLog        bool
	Debug        bool
	DebugImap    bool
	Threads      int
	ExitWhenOver int64
	AddHeader    bool // добавить заголовок X-IMAPSYNC при переносе

	// Служебное.
	ShowVersion bool
	ShowHelp    bool
}

// New возвращает Options со значениями по умолчанию, как в imapsync.
func New() *Options {
	return &Options{
		UseHeader:        []string{"Message-Id", "Received"},
		AuthMech1:        "LOGIN",
		AuthMech2:        "LOGIN",
		TimeoutSec:       0,
		Threads:          1,
		SkipEmptyFolders: true,
		ErrorsMax:        50, // $ERRORS_MAX в imapsync
	}
}

// parser — вспомогательное состояние разбора.
type parser struct {
	args []string
	i    int
	// pendingValue содержит значение, заданное через --key=value.
	pendingValue string
	hasPending   bool
}

// value возвращает значение опции: сначала из --key=value, иначе следующий аргумент.
// Вызывается после чтения ключа, когда p.i уже указывает на следующий аргумент.
func (p *parser) value(key string) (string, error) {
	if p.hasPending {
		v := p.pendingValue
		p.hasPending = false
		p.pendingValue = ""
		return v, nil
	}
	if p.i >= len(p.args) {
		return "", fmt.Errorf("опция %s требует значения", key)
	}
	v := p.args[p.i]
	p.i++
	return v, nil
}

// Parse разбирает аргументы (без имени программы).
func Parse(args []string) (*Options, error) {
	o := New()
	p := &parser{args: args}

	// Явно заданные authmech (в imapsync: authmech ||= authuser ? 'PLAIN' : 'LOGIN').
	var authmech1Set, authmech2Set bool

	for p.i < len(p.args) {
		a := p.args[p.i]
		p.i++
		if !strings.HasPrefix(a, "-") {
			// позиционный аргумент игнорируем (imapsync их не имеет)
			continue
		}
		key := strings.TrimLeft(a, "-")
		p.hasPending = false
		p.pendingValue = ""
		if idx := strings.Index(key, "="); idx >= 0 {
			p.pendingValue = key[idx+1:]
			key = key[:idx]
			p.hasPending = true
		}

		// get — короткий помощник для опций со значением.
		get := func() (string, error) { return p.value("--" + key) }

		switch key {
		case "host1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Host1 = v
		case "host2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Host2 = v
		case "user1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.User1 = v
		case "user2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.User2 = v
		case "password1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Password1 = v
		case "password2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Password2 = v
		case "port1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.Port1 = n
		case "port2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.Port2 = n
		case "authmech1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.AuthMech1 = strings.ToUpper(v)
			authmech1Set = true
		case "authmech2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.AuthMech2 = strings.ToUpper(v)
			authmech2Set = true
		case "authuser1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.AuthUser1 = v
		case "authuser2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.AuthUser2 = v
		case "proxyauth1":
			o.ProxyAuth1 = true
		case "proxyauth2":
			o.ProxyAuth2 = true

		case "ssl1":
			o.SSL1 = true
		case "ssl2":
			o.SSL2 = true
		case "tls1":
			o.TLS1 = true
		case "tls2":
			o.TLS2 = true
		case "ssl-insecure", "no-ssl-check":
			o.Insecure = true
		case "timeout1", "timeout2", "timeout":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.TimeoutSec = n
		case "compress1":
			o.Compress1 = true
		case "compress2":
			o.Compress2 = true

		case "useheader":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.UseHeader = append(o.UseHeader, v)
		case "folder":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Folder = append(o.Folder, v)
		case "folderrec":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.FolderRec = append(o.FolderRec, v)
		case "subfolder1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Subfolder1 = v
		case "subfolder2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Subfolder2 = v
		case "f1f2":
			v1, e := get()
			if e != nil {
				return nil, e
			}
			v2, e := get()
			if e != nil {
				return nil, e
			}
			o.F1F2 = append(o.F1F2, [2]string{v1, v2})
		case "automap":
			o.Automap = true
		case "exclude1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Exclude1 = append(o.Exclude1, v)
		case "exclude-all":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.ExcludeAll = append(o.ExcludeAll, v)
		case "prefix1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Prefix1 = v
		case "prefix2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Prefix2 = v

		case "delete1":
			o.Delete1 = true
		case "expunge1":
			o.Expunge1 = true
		case "delete2":
			o.Delete2 = true
		case "expunge2":
			o.Expunge2 = true
		case "delete2folders":
			o.Delete2Folders = true
		case "delete1emptyfolders":
			o.Delete1EmptyFolders = true
		case "subscribe2":
			o.Subscribe2 = true

		case "noresyncflags":
			o.NoResyncFlags = true

		case "minsize":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.ParseInt(v, 10, 64)
			if e2 != nil {
				return nil, e2
			}
			o.MinSize = n
		case "maxsize":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.ParseInt(v, 10, 64)
			if e2 != nil {
				return nil, e2
			}
			o.MaxSize = n
		case "minage":
			v, e := get()
			if e != nil {
				return nil, e
			}
			f, e2 := strconv.ParseFloat(v, 64)
			if e2 != nil {
				return nil, e2
			}
			o.MinAge = f
		case "maxage":
			v, e := get()
			if e != nil {
				return nil, e
			}
			f, e2 := strconv.ParseFloat(v, 64)
			if e2 != nil {
				return nil, e2
			}
			o.MaxAge = f
		case "search":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Search = v
		case "skipmess":
			v, e := get()
			if e != nil {
				return nil, e
			}
			// Проверяем регулярное выражение сразу, как это делает imapsync
			// (eval на строке " " при разборе опций).
			re, e2 := regexp.Compile(v)
			if e2 != nil {
				return nil, fmt.Errorf("некорректное регулярное выражение в --skipmess %q: %w", v, e2)
			}
			o.SkipMess = append(o.SkipMess, v)
			o.SkipMessRe = append(o.SkipMessRe, re)
		case "maxmessagespersecond":
			v, e := get()
			if e != nil {
				return nil, e
			}
			f, e2 := strconv.ParseFloat(v, 64)
			if e2 != nil {
				return nil, e2
			}
			o.MaxMessagesPerSecond = f
		case "maxbytespersecond":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.ParseInt(v, 10, 64)
			if e2 != nil {
				return nil, e2
			}
			o.MaxBytesPerSecond = n
		case "maxbytesafter":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.ParseInt(v, 10, 64)
			if e2 != nil {
				return nil, e2
			}
			o.MaxBytesAfter = n
		case "errorsmax":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.ErrorsMax = n

		case "dry":
			o.Dry = true
		case "justconnect":
			o.JustConnect = true
		case "justlogin":
			o.JustLogin = true
		case "justfolders":
			o.JustFolders = true
		case "justfoldersizes":
			o.JustFoldersizes = true
		case "justbanner":
			o.JustBanner = true

		case "logfile":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Logfile = v
		case "nolog":
			o.NoLog = true
		case "debug":
			o.Debug = true
		case "debugimap":
			o.DebugImap = true
		case "threads":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.Threads = n
		case "exitwhenover":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.ParseInt(v, 10, 64)
			if e2 != nil {
				return nil, e2
			}
			o.ExitWhenOver = n
		case "addheader":
			o.AddHeader = true
		case "skipemptyfolders":
			o.SkipEmptyFolders = true
		case "noskipemptyfolders":
			o.SkipEmptyFolders = false

		case "version":
			o.ShowVersion = true
		case "help":
			o.ShowHelp = true

		default:
			return nil, fmt.Errorf("неизвестная опция: --%s", key)
		}
	}

	// --delete1 подразумевает --expunge1 (как в imapsync).
	if o.Delete1 {
		o.Expunge1 = true
	}

	// Как в imapsync: authmech по умолчанию = PLAIN, если задан authuser,
	// иначе LOGIN (но только когда authmech не задан явно).
	if o.AuthUser1 != "" && !authmech1Set {
		o.AuthMech1 = "PLAIN"
	}
	if o.AuthUser2 != "" && !authmech2Set {
		o.AuthMech2 = "PLAIN"
	}

	return o, nil
}

// Validate проверяет обязательные параметры.
func (o *Options) Validate() error {
	if o.Host1 == "" || o.User1 == "" {
		return fmt.Errorf("не заданы --host1/--user1")
	}
	if o.Host2 == "" || o.User2 == "" {
		return fmt.Errorf("не заданы --host2/--user2")
	}
	// Как в imapsync: --proxyauthN требует --authuserN.
	if o.ProxyAuth1 && o.AuthUser1 == "" {
		return fmt.Errorf("--proxyauth1 требует --authuser1")
	}
	if o.ProxyAuth2 && o.AuthUser2 == "" {
		return fmt.Errorf("--proxyauth2 требует --authuser2")
	}
	if o.ErrorsMax <= 0 {
		return fmt.Errorf("--errorsmax должно быть больше нуля")
	}
	return nil
}

// Usage печатает справку по опциям.
func Usage() string {
	return fmt.Sprintf(`imapsync-go %s — синхронизация двух IMAP-аккаунтов (аналог imapsync на Go)

Использование:
  imapsync-go --host1 H1 --user1 U1 --password1 P1 --host2 H2 --user2 U2 --password2 P2 [опции]

Основные опции:
  --host1/--host2      адрес IMAP-сервера источника/назначения
  --user1/--user2      имя пользователя
  --password1/--password2  пароль (для XOAUTH2 — access token)
  --port1/--port2      порт (по умолчанию 993 при --sslN, иначе 143)
  --ssl1/--ssl2        imap over TLS (imaps)
  --tls1/--tls2        STARTTLS
  --ssl-insecure       не проверять TLS-сертификат
  --authmech1/2        LOGIN (по умолчанию) или XOAUTH2
  --authuser1/2        пользователь для аутентификации (admin user);
                       по умолчанию включает PLAIN вместо LOGIN
  --proxyauth1/2       выполнить PROXYAUTH после логина (требует --authuserN)
  --timeout N          таймаут команды в секундах (0 = без таймаута)
  --useheader H        заголовок для определения дубликатов (повторяется)
  --folder F           синхронизировать только папку F (повторяется)
  --folderrec F        синхронизировать F и подпапки (повторяется)
  --subfolder1/2       ограничить синхронизацию подпапкой
  --f1f2 SRC DST       явное отображение папок (повторяется)
  --automap            автоматически отображать одноимённые папки
  --exclude1 P         исключить папки источника по подстроке
  --prefix1 P          удалить префикс P из имён папок источника (напр. "INBOX.")
  --prefix2 P          добавить префикс P ко всем папкам назначения
  --delete1            удалить из источника после переноса (подразумевает --expunge1)
  --delete2            удалить из назначения отсутствующие в источнике
  --delete2folders     удалить папки назначения, отсутствующие в источнике
  --subscribe2         подписаться на созданные папки назначения
  --skipemptyfolders   пустые папки host1 не создаются на host2 (по умолчанию)
  --noskipemptyfolders создавать пустые папки host1 на host2
  --noresyncflags      не пересинхронизировать флаги
  --minsize/--maxsize  фильтр по размеру сообщения (байт)
  --minage/--maxage    фильтр по возрасту сообщения (дней)
  --skipmess RE        пропустить сообщения, содержимое которых совпадает с RE (повторяется)
  --maxmessagespersecond N  ограничить скорость: сообщений в секунду
  --maxbytespersecond  N  ограничить скорость: байт в секунду
  --maxbytesafter N    байты, после которых учитывается --maxbytespersecond
  --errorsmax N        остановить при достижении N ошибок (по умолчанию 50)
  --dry                имитация без записи
  --justconnect        только подключиться и показать capabilities
  --justlogin          только подключиться и залогиниться
  --justfolders        только список папок источника
  --justfoldersizes    только размеры папок источника
  --logfile FILE       файл журнала (по умолчанию LOG_imapsync/<stamp>_<u1>_<u2>.txt)
  --nolog              не писать журнал
  --debug              подробный вывод
  --threads N          число параллельных потоков синхронизации папок
  --exitwhenover N     остановить после переноса N байт
  --addheader          добавить заголовок X-IMAPSYNC при переносе
  --version            показать версию
  --help               эта справка

Пример:
  imapsync-go --host1 imap.src.ru --user1 a --password1 p1 \
              --host2 imap.dst.ru --user2 b --password2 p2 \
              --ssl1 --ssl2 --useheader Message-Id

Платформа: %s/%s
`, Version, runtime.GOOS, runtime.GOARCH)
}
