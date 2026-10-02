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

	// Разделители и критерии поиска (как в imapsync).
	Sep1, Sep2       string // переопределение разделителя иерархии папок host1/host2
	Search1, Search2 string // дополнительные критерии IMAP SEARCH для host1/host2

	// TLS.
	SSL1, SSL2 bool // imap over TLS (imaps), порт 993 по умолчанию
	TLS1, TLS2 bool // STARTTLS поверх обычного соединения
	Insecure   bool // --ssl-insecure: не проверять сертификат
	TimeoutSec int  // таймаут команды в секундах (0 = без таймаута)
	Compress1  bool
	Compress2  bool

	// Идентификация сообщений.
	UseHeader    []string // заголовки для определения дубликатов
	SkipHeader   string   // регулярное выражение: исключать совпадающие заголовки из ключа идентичности
	SkipHeaderRe *regexp.Regexp

	// Идентификация по UID и кэширование (--useuid, --usecache).
	UseUID   bool   // использовать UID вместо заголовков для распознавания сообщений
	UseCache bool   // использовать локальный кэш сопоставления UID/сообщений
	CacheDir string // каталог кэша (по умолчанию .imapsync_cache)

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

	// Преобразования и фильтры папок (--regextrans2, --include, --exclude, --folderfirst, --folderlast).
	RegexTrans2 []string // регулярные выражения замены имён папок для host2 (s/from/to/flags)
	Include     []string // регулярные выражения для включения папок
	IncludeRe   []*regexp.Regexp
	Exclude     []string // регулярные выражения для исключения папок
	ExcludeRe   []*regexp.Regexp
	FolderFirst []string // синхронизировать эти папки в первую очередь
	FolderLast  []string // синхронизировать эти папки в последнюю очередь

	// Удаления.
	Delete1             bool // удалить из источника после переноса
	Expunge1            bool
	Delete2             bool // удалить из назначения то, чего нет в источнике
	Expunge2            bool
	Delete2Folders      bool // удалить папки назначения, отсутствующие в источнике
	Delete1EmptyFolders bool
	Subscribe2          bool // подписаться на созданные папки назначения
	UidExpunge2         bool // использовать UID EXPUNGE на host2 (включается автоматически при --delete2)
	ExpungeAfterEach    bool // выполнять expunge после каждого удаления (по умолчанию true)

	// Дедупликация и повторные копии.
	SkipCrossDuplicates bool // не копировать сообщение, если такой ключ уже встречался в ЛЮБОЙ папке host2
	Delete2Duplicates   bool // удалять дубликаты сообщений внутри папок на host2

	// Пустые папки (как в imapsync: по умолчанию skipemptyfolders = 1).
	SkipEmptyFolders bool

	// Флаги.
	NoResyncFlags      bool     // не пересинхронизировать флаги
	SyncFlagsAfterCopy bool     // синхронизировать флаги сразу после APPEND
	FilterFlags        bool     // фильтровать системные флаги, отбрасывая нестандартные (по умолчанию true)
	RegexFlag          []string // Perl-like regex замены флагов (s/from/to/)

	// Даты сообщений.
	SyncInternalDates bool // сохранять INTERNALDATE источника (по умолчанию true)

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
	MaxSleep             float64 // максимальная пауза троттлинга в секундах (по умолчанию 2.0)

	// Ограничение размера и усечение сообщений (--appendlimit, --truncmess).
	Appendlimit int64 // пропуск сообщений больше N байт (или из APPENDLIMIT сервера)
	Truncmess   int64 // усекать сообщения до N байт при превышении

	// --errorsmax: максимальное число ошибок, при котором imapsync
	// останавливается (по умолчанию 50, как в оригинале).
	ErrorsMax int

	// Режимы.
	Dry             bool
	Dry1            bool // true по умолчанию в dry-режиме, --nodry1 отключает пропуск выборки сообщений с host1
	JustConnect     bool
	JustLogin       bool
	JustFolders     bool
	JustFoldersizes bool
	JustBanner      bool

	// Прочее.
	Logfile      string
	Logdir       string // каталог журнала по умолчанию (LOG_imapsync)
	NoLog        bool
	Debug        bool
	DebugImap    bool
	Threads      int
	ExitWhenOver int64
	AddHeader    bool // добавить заголовок X-IMAPSYNC при переносе

	// Параметры TLS-рукопожатия OpenSSL (--sslargsN), как в imapsync.
	// Значения вида "SSL_verify_mode=0"; --nosslargs отключает их все.
	SslArgs1, SslArgs2 []string
	NoSslArgs          bool

	// Явное отключение TLS (--nosslN/--notlsN), как в imapsync.
	NoSSL1, NoSSL2 bool
	NoTLS1, NoTLS2 bool

	// OAuth (--oauthaccesstokenN, --oauthdirectN): токен подставляется
	// в PasswordN, а AuthMechN становится XOAUTH2.
	OAuthAccessToken1, OAuthAccessToken2 string
	OAuthDirect1, OAuthDirect2           string

	// Пресеты провайдеров (--dominoN, --exchangeN, --officeN).
	Exchange1, Exchange2 bool
	Office1, Office2     bool
	Domino1, Domino2     bool

	// Фильтры/преобразования содержимого сообщения.
	DisarmReadReceipts bool     // --disarmreadreceipts
	RegexMess          []string // --regexmess: Perl-подобные замены в сообщении

	// Лимиты буфера/строки.
	BufferSize    int // --buffersize
	MaxLineLength int // --maxlinelength

	// Даты/флаги сообщений.
	IDateFromHeader  bool // --idatefromheader
	FilterBuggyFlags bool // --filterbuggyflags

	// Дедупликация/качество.
	SyncDuplicates       bool // --syncduplicates
	AllowSizeMismatch    bool // --allowsizemismatch
	SkipSize             bool // --skipsize
	DebugCrossDuplicates bool // --debugcrossduplicates

	// Подписки и ACL/метки.
	Subscribed   bool // --subscribed: только подписанные папки
	SubscribeAll bool // --subscribeall
	Subscribe    bool // --subscribe: подписывать на host2 перенесённые подписанные папки (по умолчанию)
	NoSyncAcls   bool // --nosyncacls
	SyncLabels   bool // --synclabels
	ResyncLabels bool // --resynclabels
	Labels1      bool // --labels1
	Labels2      bool // --labels2

	// Устойчивость соединения.
	Keepalive1, Keepalive2     bool
	NoKeepalive1, NoKeepalive2 bool
	NoAbilityToSearch          bool // --noabletosearch
	NoAbilityToSearch1         bool
	NoAbilityToSearch2         bool

	// Отладка (флаги из whitelist проекта; часть — no-op).
	DebugFolders           bool
	DebugContent           bool
	DebugFlags             bool
	DebugSSL1, DebugSSL2   bool
	DebugIMAP1, DebugIMAP2 bool
	NoErrorsDump           bool
	NoModulesVersion       bool
	ModulesVersion         bool

	// Служебные файлы/отчёты.
	PidFile     string // --pidfile
	TmpDir      string // --tmpdir
	EmailReport string // --emailreport
	ExitStatus0 bool   // --exitstatus0
	Tests       bool   // --tests
	Info        bool   // --info

	// Таймауты по хостам (--timeout1/--timeout2).
	Timeout1, Timeout2 int

	// Попытки переподключения при обрыве соединения (--reconnectretry1/2),
	// как $DEFAULT_NB_RECONNECT_PER_IMAP_COMMAND в imapsync.
	Reconnect1, Reconnect2 int

	// Пресеты Gmail (--gmail1/--gmail2).
	Gmail1, Gmail2 bool

	// Прочие опции проекта, принимаемые для совместимости.
	Passfile1, Passfile2  string // --passfileN: файл с паролем (первая строка)
	Domain1, Domain2      string // --domainN: домен для NTLM
	Authmd51, Authmd52    bool   // --authmd5N
	ShowPasswords         bool   // --showpasswords
	NomixFolders          bool   // --nomixfolders
	Noid                  bool   // --noid: не отправлять IMAP ID
	SyncAcls              bool   // --syncacls
	NoExpunge1            bool   // --noexpunge1
	NoExpunge2            bool   // --noexpunge2
	NoFoldersizes         bool   // --nofoldersizes
	NoFoldersizesAtEnd    bool   // --nofoldersizesatend
	PidFileLocking        bool   // --pidfilelocking
	Abort                 bool   // --abort
	EmailReport1          bool   // --emailreport1
	EmailReport2          bool   // --emailreport2
	NoEmailReport1        bool   // --noemailreport1
	NoEmailReport2        bool   // --noemailreport2
	PipeMess              []string
	Delete2FoldersOnly    string // --delete2foldersonly RE
	Delete2FoldersButNot  string // --delete2foldersbutnot RE
	NoSkipCrossDuplicates bool   // --noskipcrossduplicates
	NoSubscribe           bool   // --nosubscribe
	NoSyncLabels          bool   // --nosynclabels
	NoResyncLabels        bool   // --noresynclabels

	// Perl-отрицания, отключающие добавления пресетов провайдеров.
	NoRegexFlag  bool // --noregexflag
	NoRegexMess  bool // --noregexmess
	NoF1F2       bool // --nof1f2
	NoExcludeOpt bool // --noexclude

	// Отслеживание явных установок для пресетов провайдеров
	// (семантика Perl: ||= и defined).
	ssl1Set, ssl2Set               bool
	skipcrossduplicatesSet         bool
	synclabelsSet, resynclabelsSet bool
	idatefromheaderSet             bool
	maxSleepSet                    bool
	automapSet                     bool
	addHeaderSet                   bool
	expunge1Set                    bool
	usecacheSet                    bool

	// Служебное.
	ShowVersion bool
	ShowHelp    bool
}

// sslArgsVerifyModeZero — в списке --sslargsN есть SSL_verify_mode=0.
// В Perl imapsync этот аргумент отключает проверку сертификата сервера;
// Go-движок трактует его как InsecureSkipVerify для данного host.
func sslArgsVerifyModeZero(args []string) bool {
	for _, a := range args {
		kv := strings.SplitN(a, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(kv[0]), "SSL_verify_mode") &&
			strings.TrimSpace(kv[1]) == "0" {
			return true
		}
	}
	return false
}

// SSLInsecure1/SSLInsecure2 — проверять ли сертификат host1/host2:
// true, если задано --ssl-insecure или для данного host в --sslargsN
// указан SSL_verify_mode=0 (совместимость с Perl imapsync, где агенты
// работают через туннель 127.0.0.1 с самоподписанными сертификатами).
func (o *Options) SSLInsecure1() bool {
	return o.Insecure || sslArgsVerifyModeZero(o.SslArgs1)
}

func (o *Options) SSLInsecure2() bool {
	return o.Insecure || sslArgsVerifyModeZero(o.SslArgs2)
}

// New возвращает Options со значениями по умолчанию, как в imapsync.
func New() *Options {
	return &Options{
		UseHeader:         []string{"Message-Id", "Received"},
		AuthMech1:         "LOGIN",
		AuthMech2:         "LOGIN",
		TimeoutSec:        0,
		Threads:           1,
		Reconnect1:        3, // $DEFAULT_NB_RECONNECT_PER_IMAP_COMMAND
		Reconnect2:        3,
		SkipEmptyFolders:  true,
		ErrorsMax:         50,  // $ERRORS_MAX в imapsync
		MaxSleep:          2.0, // $MAX_SLEEP в imapsync = 2 сек
		SyncInternalDates: true,
		FilterFlags:       true,
		ExpungeAfterEach:  true,
		Dry1:              true, // синхронизируется с Dry при Parse
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

// optionalValue молча потребляет следующий аргумент, если он не является
// опцией. Нужно для булевых опций, которые проект генерирует как строковые
// (например "--proxyauth1 true" или "--debugssl 4").
func (p *parser) optionalValue() {
	if p.hasPending {
		p.hasPending = false
		p.pendingValue = ""
		return
	}
	if p.i < len(p.args) && !strings.HasPrefix(p.args[p.i], "-") {
		p.i++
	}
}

// hasValue сообщает, есть ли у текущей опции значение (явное или следующий
// аргумент, не являющийся опцией).
func (p *parser) hasValue() bool {
	if p.hasPending {
		return true
	}
	return p.i < len(p.args) && !strings.HasPrefix(p.args[p.i], "-")
}

// splitHostPort разбирает значение --hostN в стиле imapsync, где допускаются
// формы "host", "host:port" и "host:port/folderrec".
func splitHostPort(v string) (host string, port int, folderRec string, err error) {
	i := strings.Index(v, ":")
	if i < 0 {
		return v, 0, "", nil
	}
	host = v[:i]
	rest := v[i+1:]
	folderRec = ""
	if j := strings.Index(rest, "/"); j >= 0 {
		folderRec = rest[j+1:]
		rest = rest[:j]
	}
	n, e := strconv.Atoi(rest)
	if e != nil {
		return "", 0, "", fmt.Errorf("некорректный порт в значении --host %q", v)
	}
	return host, n, folderRec, nil
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
			host, port, frec, e2 := splitHostPort(v)
			if e2 != nil {
				return nil, e2
			}
			o.Host1 = host
			if port != 0 {
				o.Port1 = port
			}
			if frec != "" {
				o.FolderRec = append(o.FolderRec, frec)
			}
		case "host2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			host, port, frec, e2 := splitHostPort(v)
			if e2 != nil {
				return nil, e2
			}
			o.Host2 = host
			if port != 0 {
				o.Port2 = port
			}
			if frec != "" {
				o.FolderRec = append(o.FolderRec, frec)
			}
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
			p.optionalValue() // проект генерирует "--proxyauth1 <значение>"
		case "proxyauth2":
			o.ProxyAuth2 = true
			p.optionalValue()

		case "sep1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Sep1 = v
		case "sep2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Sep2 = v
		case "search1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Search1 = v
		case "search2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Search2 = v

		case "ssl1":
			o.SSL1 = true
			o.ssl1Set = true
		case "ssl2":
			o.SSL2 = true
			o.ssl2Set = true
		case "nossl1":
			o.SSL1 = false
			o.ssl1Set = true
		case "nossl2":
			o.SSL2 = false
			o.ssl2Set = true
		case "tls1":
			o.TLS1 = true
		case "tls2":
			o.TLS2 = true
		case "notls1":
			o.TLS1 = false
		case "notls2":
			o.TLS2 = false
		case "ssl-insecure", "no-ssl-check":
			o.Insecure = true
		case "sslargs1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.SslArgs1 = append(o.SslArgs1, v)
		case "sslargs2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.SslArgs2 = append(o.SslArgs2, v)
		case "nosslargs":
			o.NoSslArgs = true
			o.SslArgs1, o.SslArgs2 = nil, nil
		case "timeout1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.Timeout1 = n
			o.TimeoutSec = n
		case "timeout2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.Timeout2 = n
			o.TimeoutSec = n
		case "reconnectretry1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.Reconnect1 = n
		case "reconnectretry2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.Reconnect2 = n
		case "timeout":
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

		case "useheader", "userheader":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.UseHeader = append(o.UseHeader, v)
		case "skipheader":
			v, e := get()
			if e != nil {
				return nil, e
			}
			re, err := regexp.Compile("(?i)" + v)
			if err != nil {
				return nil, fmt.Errorf("некорректное регулярное выражение в --skipheader %q: %w", v, err)
			}
			o.SkipHeader = v
			o.SkipHeaderRe = re
		case "useuid", "useid":
			o.UseUID = true
		case "nouseuid":
			o.UseUID = false
		case "usecache", "usechache":
			o.UseCache = true
			o.usecacheSet = true
		case "nousecache", "nousechache":
			o.UseCache = false
			o.usecacheSet = true
		case "cachedir":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.CacheDir = v
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
			// imapsync и проект используют форму "src=dst" одним токеном.
			if i := strings.Index(v1, "="); i >= 0 {
				o.F1F2 = append(o.F1F2, [2]string{v1[:i], v1[i+1:]})
				break
			}
			v2, e := get()
			if e != nil {
				return nil, e
			}
			o.F1F2 = append(o.F1F2, [2]string{v1, v2})
		case "nof1f2":
			o.NoF1F2 = true
		case "automap":
			o.Automap = true
			o.automapSet = true
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

		case "regextrans2", "regtrans2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.RegexTrans2 = append(o.RegexTrans2, v)
		case "noinclude":
			// no-op: совместимость с синтаксисом imapsync
		case "include":
			v, e := get()
			if e != nil {
				return nil, e
			}
			re, err := regexp.Compile(v)
			if err != nil {
				return nil, fmt.Errorf("некорректное регулярное выражение в --include %q: %w", v, err)
			}
			o.Include = append(o.Include, v)
			o.IncludeRe = append(o.IncludeRe, re)
		case "noexclude":
			o.NoExcludeOpt = true
		case "exclude":
			v, e := get()
			if e != nil {
				return nil, e
			}
			re, err := regexp.Compile(v)
			if err != nil {
				return nil, fmt.Errorf("некорректное регулярное выражение в --exclude %q: %w", v, err)
			}
			o.Exclude = append(o.Exclude, v)
			o.ExcludeRe = append(o.ExcludeRe, re)
		case "folderfirst":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.FolderFirst = append(o.FolderFirst, v)
		case "folderlast":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.FolderLast = append(o.FolderLast, v)
		case "nofolderlast":
			o.FolderLast = nil

		case "delete1":
			o.Delete1 = true
		case "expunge1":
			o.Expunge1 = true
			o.expunge1Set = true
		case "noexpunge1":
			o.Expunge1 = false
			o.expunge1Set = true
		case "noexpunge2":
			o.NoExpunge2 = true
			o.Expunge2 = false
		case "delete2":
			o.Delete2 = true
		case "expunge2":
			o.Expunge2 = true
		case "delete2folders":
			o.Delete2Folders = true
		case "delete2foldersonly":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Delete2FoldersOnly = v
			o.Delete2Folders = true
		case "delete2foldersbutnot":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Delete2FoldersButNot = v
			o.Delete2Folders = true
		case "delete1emptyfolders":
			o.Delete1EmptyFolders = true
		case "subscribe2":
			o.Subscribe2 = true
		case "uidexpunge2":
			o.UidExpunge2 = true
		case "nouidexpunge2":
			o.UidExpunge2 = false
		case "expungeaftereach":
			o.ExpungeAfterEach = true
		case "noexpungeaftereach":
			o.ExpungeAfterEach = false

		case "skipcrossduplicates":
			o.SkipCrossDuplicates = true
			o.skipcrossduplicatesSet = true
		case "noskipcrossduplicates":
			o.SkipCrossDuplicates = false
			o.skipcrossduplicatesSet = true
		case "delete2duplicates":
			o.Delete2Duplicates = true

		case "noresyncflags":
			o.NoResyncFlags = true
		case "resyncflags":
			o.NoResyncFlags = false
		case "filterbuggyflags":
			o.FilterBuggyFlags = true
		case "syncflagsaftercopy":
			o.SyncFlagsAfterCopy = true
		case "filterflags":
			o.FilterFlags = true
		case "nofilterflags":
			o.FilterFlags = false
		case "regexflag":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.RegexFlag = append(o.RegexFlag, v)
		case "noregexflag":
			o.NoRegexFlag = true

		case "syncinternaldates":
			o.SyncInternalDates = true
		case "nosyncinternaldates":
			o.SyncInternalDates = false
		case "idatefromheader", "adtefromheader":
			o.IDateFromHeader = true
			o.idatefromheaderSet = true
		case "noidatefromheader":
			o.IDateFromHeader = false
			o.idatefromheaderSet = true

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
		case "maxsleep":
			v, e := get()
			if e != nil {
				return nil, e
			}
			f, e2 := strconv.ParseFloat(v, 64)
			if e2 != nil {
				return nil, e2
			}
			o.MaxSleep = f
		case "appendlimit":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.ParseInt(v, 10, 64)
			if e2 != nil {
				return nil, e2
			}
			o.Appendlimit = n
		case "truncmess":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.ParseInt(v, 10, 64)
			if e2 != nil {
				return nil, e2
			}
			o.Truncmess = n
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
			o.Dry1 = true
		case "dry1":
			o.Dry1 = true
		case "nodry1":
			o.Dry1 = false
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
		case "logdir":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Logdir = v
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
			o.addHeaderSet = true
		case "noaddheader":
			o.AddHeader = false
			o.addHeaderSet = true
		case "skipemptyfolders":
			o.SkipEmptyFolders = true
		case "noskipemptyfolders":
			o.SkipEmptyFolders = false

		// --- OAuth -------------------------------------------------------
		case "oauthaccesstoken1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.OAuthAccessToken1 = v
			o.Password1 = v
			o.AuthMech1 = "XOAUTH2"
			authmech1Set = true
		case "oauthaccesstoken2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.OAuthAccessToken2 = v
			o.Password2 = v
			o.AuthMech2 = "XOAUTH2"
			authmech2Set = true
		case "oauthdirect1", "authdirect1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.OAuthDirect1 = v
			o.Password1 = v
			o.AuthMech1 = "XOAUTH2"
			authmech1Set = true
		case "oauthdirect2", "authdirect2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.OAuthDirect2 = v
			o.Password2 = v
			o.AuthMech2 = "XOAUTH2"
			authmech2Set = true

		// --- Пресеты провайдеров ----------------------------------------
		case "gmail1":
			o.Gmail1 = true
		case "gmail2":
			o.Gmail2 = true
		case "office1":
			o.Office1 = true
		case "office2":
			o.Office2 = true
		case "exchange1":
			o.Exchange1 = true
		case "exchange2":
			o.Exchange2 = true
		case "domino1":
			o.Domino1 = true
		case "domino2":
			o.Domino2 = true
		case "disarmreadreceipts":
			o.DisarmReadReceipts = true
		case "nodisarmreadreceipts":
			o.DisarmReadReceipts = false
		case "regexmess":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.RegexMess = append(o.RegexMess, v)
		case "noregexmess":
			o.NoRegexMess = true

		// --- Подписки, ACL, метки ---------------------------------------
		case "subscribed", "subcribed":
			o.Subscribed = true
		case "subscribe":
			o.Subscribe = true
		case "nosubscribe":
			o.Subscribe = false
		case "subscribeall":
			o.SubscribeAll = true
		case "nosyncacls":
			o.NoSyncAcls = true
		case "syncacls":
			o.SyncAcls = true
		case "synclabels":
			o.SyncLabels = true
			o.synclabelsSet = true
		case "nosynclabels":
			o.SyncLabels = false
			o.synclabelsSet = true
		case "resynclabels":
			o.ResyncLabels = true
			o.resynclabelsSet = true
		case "noresynclabels":
			o.ResyncLabels = false
			o.resynclabelsSet = true
		case "labels1":
			o.Labels1 = true
		case "labels2":
			o.Labels2 = true

		// --- Устойчивость соединения -------------------------------------
		case "keepalive1":
			o.Keepalive1 = true
		case "keepalive2":
			o.Keepalive2 = true
		case "nokeepalive1":
			o.NoKeepalive1 = true
			o.Keepalive1 = false
		case "nokeepalive2":
			o.NoKeepalive2 = true
			o.Keepalive2 = false
		case "noabletosearch":
			o.NoAbilityToSearch = true
			o.NoAbilityToSearch1 = true
			o.NoAbilityToSearch2 = true
		case "noabletosearch1":
			o.NoAbilityToSearch1 = true
		case "noabletosearch2":
			o.NoAbilityToSearch2 = true

		// --- Лимиты буфера/строки ---------------------------------------
		case "buffersize":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.BufferSize = n
		case "maxlinelength":
			v, e := get()
			if e != nil {
				return nil, e
			}
			n, e2 := strconv.Atoi(v)
			if e2 != nil {
				return nil, e2
			}
			o.MaxLineLength = n

		// --- Дедупликация/качество --------------------------------------
		case "syncduplicates":
			o.SyncDuplicates = true
		case "allowsizemismatch":
			o.AllowSizeMismatch = true
		case "skipsize":
			o.SkipSize = true
		case "debugcrossduplicates":
			o.DebugCrossDuplicates = true

		// --- Отладка -----------------------------------------------------
		case "debugfolders":
			o.DebugFolders = true
		case "debugcontent":
			o.DebugContent = true
		case "debugflags":
			o.DebugFlags = true
		case "debugssl":
			// imapsync допускает уровень 0..4; проект передаёт int.
			if p.hasValue() {
				v, e := get()
				if e != nil {
					return nil, e
				}
				if n, e2 := strconv.Atoi(v); e2 == nil && n > 0 {
					o.DebugSSL1 = true
					o.DebugSSL2 = true
				}
			} else {
				o.DebugSSL1 = true
				o.DebugSSL2 = true
			}
		case "debugssl1":
			o.DebugSSL1 = true
		case "debugssl2":
			o.DebugSSL2 = true
		case "debugimap1":
			o.DebugIMAP1 = true
			o.DebugImap = true
		case "debugimap2":
			o.DebugIMAP2 = true
			o.DebugImap = true
		case "noerrorsdump":
			o.NoErrorsDump = true
		case "nomodulesversion", "no-modulesversion":
			o.NoModulesVersion = true
		case "modulesversion":
			o.ModulesVersion = true

		// --- Прочее, принимаемое для совместимости -----------------------
		case "passfile1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Passfile1 = v
		case "passfile2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Passfile2 = v
		case "domain1":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Domain1 = v
		case "domain2":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.Domain2 = v
		case "authmd51":
			o.Authmd51 = true
		case "authmd52":
			o.Authmd52 = true
		case "showpasswords":
			o.ShowPasswords = true
		case "nomixfolders":
			o.NomixFolders = true
		case "noid":
			o.Noid = true
		case "nofoldersizes":
			o.NoFoldersizes = true
		case "nofoldersizesatend":
			o.NoFoldersizesAtEnd = true
		case "pidfile":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.PidFile = v
		case "pidfilelocking":
			o.PidFileLocking = true
		case "tmpdir":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.TmpDir = v
		case "abort":
			o.Abort = true
		case "emailreport1":
			o.EmailReport1 = true
		case "emailreport2":
			o.EmailReport2 = true
		case "noemailreport1":
			o.NoEmailReport1 = true
		case "noemailreport2":
			o.NoEmailReport2 = true
		case "pipemess":
			v, e := get()
			if e != nil {
				return nil, e
			}
			o.PipeMess = append(o.PipeMess, v)
		case "exitstatus0":
			o.ExitStatus0 = true
		case "tests":
			o.Tests = true
		case "info":
			o.Info = true

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

	// --delete2 и --delete2duplicates подразумевают --uidexpunge2 (как в imapsync).
	if o.Delete2 || o.Delete2Duplicates {
		o.UidExpunge2 = true
	}

	// --useuid подразумевает --usecache (если не отключен явно через nousecache).
	if o.UseUID {
		o.UseCache = true
	}

	// Как в imapsync: authmech по умолчанию = PLAIN, если задан authuser,
	// иначе LOGIN (но только когда authmech не задан явно).
	if o.AuthUser1 != "" && !authmech1Set {
		o.AuthMech1 = "PLAIN"
	}
	if o.AuthUser2 != "" && !authmech2Set {
		o.AuthMech2 = "PLAIN"
	}

	// Пресеты провайдеров применяются в том же порядке, что и в imapsync:
	// gmail12 → gmail1 → gmail2 → office1 → office2 → exchange1 → exchange2
	// → domino1 → domino2.
	if o.Gmail1 && o.Gmail2 {
		applyGmail12(o)
	}
	if o.Gmail1 {
		applyGmail1(o)
	}
	if o.Gmail2 {
		applyGmail2(o)
	}
	if o.Office1 {
		applyOffice1(o)
	}
	if o.Office2 {
		applyOffice2(o)
	}
	// --exchange1 в imapsync ничего не делает.
	if o.Exchange2 {
		applyExchange2(o)
	}
	if o.Domino1 {
		o.Sep1 = `\`
		o.Prefix1 = ""
	}
	if o.Domino2 {
		o.Sep2 = `\`
		o.Prefix2 = ""
		o.RegexTrans2 = append(o.RegexTrans2, `s,^Inbox\\(.*),$1,i`)
	}

	// Как в Perl imapsync ($SSL1 ||= $PORT1 == 993): порт 993 подразумевает
	// imap-over-TLS, если ssl/tls не заданы явно (--sslN/--nosslN). Без этого
	// Go-движок подключается к 993 открытым текстом, и сервер (Yandex, Mail.ru
	// и т.п.) сбрасывает соединение (EOF) — перенос падает с 0 сообщений.
	// Явная установка (--sslN/--nosslN, sslNSet) не переопределяется.
	if o.Port1 == 993 && !o.ssl1Set {
		o.SSL1 = true
	}
	if o.Port2 == 993 && !o.ssl2Set {
		o.SSL2 = true
	}

	return o, nil
}

// gmailFolderLast — папки Gmail, синхронизируемые в последнюю очередь
// (как в imapsync @folderlast).
var gmailFolderLast = []string{
	"[Gmail]/Sent Mail", "[Gmail]/Important", "[Gmail]/Starred",
	"[Gmail]/Drafts", "[Gmail]/Trash", "[Gmail]/Spam",
	"[Gmail]/Chats", "[Gmail]/All Mail",
}

// addExcludeRegexp добавляет регулярное выражение исключения папок
// (аналог push @exclude в imapsync).
func addExcludeRegexp(o *Options, pattern string) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return
	}
	o.Exclude = append(o.Exclude, pattern)
	o.ExcludeRe = append(o.ExcludeRe, re)
}

// applyGmail12 — пресет --gmail1 --gmail2 (sub gmail12 в imapsync).
func applyGmail12(o *Options) {
	if o.Host1 == "" {
		o.Host1 = "imap.gmail.com"
	}
	if !o.ssl1Set {
		o.SSL1 = true
	}
	if o.Host2 == "" {
		o.Host2 = "imap.gmail.com"
	}
	if !o.ssl2Set {
		o.SSL2 = true
	}
	if o.MaxBytesPerSecond == 0 {
		o.MaxBytesPerSecond = 300_000
	}
	if o.MaxBytesAfter == 0 {
		o.MaxBytesAfter = 3_000_000_000
	}
	if !o.automapSet {
		o.Automap = true
	}
	if !o.maxSleepSet {
		o.MaxSleep = 100
	}
	if !o.skipcrossduplicatesSet {
		o.SkipCrossDuplicates = false
	}
	if !o.synclabelsSet {
		o.SyncLabels = true
	}
	if !o.resynclabelsSet {
		o.ResyncLabels = true
	}
	if !o.idatefromheaderSet {
		o.IDateFromHeader = true
	}
	o.UseHeader = append(o.UseHeader, "X-Gmail-Received", "Message-Id")
	if !o.NoExcludeOpt {
		addExcludeRegexp(o, `\[Gmail\]$`)
	}
	o.FolderLast = append(o.FolderLast, gmailFolderLast...)
}

// applyGmail1 — пресет --gmail1 (sub gmail1 в imapsync).
func applyGmail1(o *Options) {
	if o.Host1 == "" {
		o.Host1 = "imap.gmail.com"
	}
	if !o.ssl1Set {
		o.SSL1 = true
	}
	if o.MaxBytesPerSecond == 0 {
		o.MaxBytesPerSecond = 300_000
	}
	if o.MaxBytesAfter == 0 {
		o.MaxBytesAfter = 3_000_000_000
	}
	if !o.automapSet {
		o.Automap = true
	}
	if !o.maxSleepSet {
		o.MaxSleep = 100
	}
	if !o.skipcrossduplicatesSet {
		o.SkipCrossDuplicates = true
	}
	o.UseHeader = append(o.UseHeader, "X-Gmail-Received", "Message-Id")
	o.RegexTrans2 = append(o.RegexTrans2, `s,\[Gmail\].,,`)
	o.FolderLast = append(o.FolderLast, gmailFolderLast...)
}

// applyGmail2 — пресет --gmail2 (sub gmail2 в imapsync).
func applyGmail2(o *Options) {
	if o.Host2 == "" {
		o.Host2 = "imap.gmail.com"
	}
	if !o.ssl2Set {
		o.SSL2 = true
	}
	if o.MaxBytesPerSecond == 0 {
		o.MaxBytesPerSecond = 300_000
	}
	if o.MaxBytesAfter == 0 {
		o.MaxBytesAfter = 3_000_000_000
	}
	if !o.automapSet {
		o.Automap = true
	}
	if !o.expunge1Set {
		o.Expunge1 = true
	}
	if !o.addHeaderSet {
		o.AddHeader = true
	}
	if !o.maxSleepSet {
		o.MaxSleep = 100
	}
	if !o.idatefromheaderSet {
		o.IDateFromHeader = true
	}
	if !o.NoExcludeOpt {
		addExcludeRegexp(o, `\[Gmail\]$`)
	}
	o.UseHeader = append(o.UseHeader, "Message-Id")
	o.RegexTrans2 = append(o.RegexTrans2,
		`s,\[Gmail\].,,`,
		`s,^ +| +$,,g`,
		`s,/ +| +/,/,g`,
		`s/['\^"]/_/g`,
	)
	o.FolderLast = append(o.FolderLast, gmailFolderLast...)
}

// applyOffice1 — пресет --office1 (Office 365 на host1).
func applyOffice1(o *Options) {
	if o.Host1 == "" {
		o.Host1 = "outlook.office365.com"
	}
	if !o.ssl1Set {
		o.SSL1 = true
	}
	if !o.NoExcludeOpt {
		addExcludeRegexp(o, `^Files$`)
	}
}

// applyOffice2 — пресет --office2 (Office 365 на host2).
func applyOffice2(o *Options) {
	if o.Host2 == "" {
		o.Host2 = "outlook.office365.com"
	}
	if !o.ssl2Set {
		o.SSL2 = true
	}
	if o.MaxSize == 0 {
		o.MaxSize = 45_000_000
	}
	if o.MaxMessagesPerSecond == 0 {
		o.MaxMessagesPerSecond = 4
	}
	o.DisarmReadReceipts = true
	if !o.NoRegexMess {
		o.RegexMess = append(o.RegexMess, `s,(.{10239}),$1`+"\r\n"+`,g`)
	}
	if !o.NoF1F2 {
		o.F1F2 = append(o.F1F2, [2]string{"Files", "Files_renamed_by_imapsync"})
	}
}

// applyExchange2 — пресет --exchange2 (Exchange на host2).
func applyExchange2(o *Options) {
	if o.MaxSize == 0 {
		o.MaxSize = 10_000_000
	}
	if o.MaxMessagesPerSecond == 0 {
		o.MaxMessagesPerSecond = 4
	}
	o.DisarmReadReceipts = true
	if !o.NoRegexFlag {
		o.RegexFlag = append(o.RegexFlag, `s/\\Flagged//g`)
	}
	if !o.NoRegexMess {
		o.RegexMess = append(o.RegexMess, `s,(.{10239}),$1`+"\r\n"+`,g`)
	}
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
	// Конфликт в imapsync: "can not have both --usecache and --skipcrossduplicates"
	if o.UseCache && o.SkipCrossDuplicates {
		return fmt.Errorf("нельзя одновременно использовать --usecache и --skipcrossduplicates")
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
  --skipheader RE      исключить заголовки, совпадающие с RE, из ключа идентичности
  --sep1/--sep2        переопределить разделитель иерархии папок host1/host2
  --search1/--search2  критерии IMAP SEARCH для фильтрации UID (UNSEEN, FLAGGED и т.д.)
  --folder F           синхронизировать только папку F (повторяется)
  --folderrec F        синхронизировать F и подпапки (повторяется)
  --subfolder1/2       ограничить синхронизацию подпапкой
  --f1f2 SRC DST       явное отображение папок (повторяется)
  --automap            автоматически отображать одноимённые папки
  --exclude1 P         исключить папки источника по подстроке
  --include RE         включить только папки, совпадающие с regex RE (повторяется)
  --exclude RE         исключить папки, совпадающие с regex RE (повторяется)
  --folderfirst F      синхронизировать папку F в первую очередь (повторяется)
  --folderlast F       синхронизировать папку F в последнюю очередь (повторяется)
  --regextrans2 S      Perl-подобная замена имён папок s/from/to/flags (повторяется)
  --prefix1 P          удалить префикс P из имён папок источника (напр. "INBOX.")
  --prefix2 P          добавить префикс P ко всем папкам назначения
  --useuid             использовать UID+кэш для распознавания сообщений (подразумевает --usecache)
  --usecache           использовать локальный SQLite-кэш сопоставления UID
  --cachedir DIR       каталог кэша (по умолчанию .imapsync_cache)
  --delete1            удалить из источника после переноса (подразумевает --expunge1)
  --delete2            удалить из назначения отсутствующие в источнике (подразумевает --uidexpunge2)
  --delete2duplicates  удалить дубликаты внутри папок host2 (подразумевает --uidexpunge2)
  --delete2folders     удалить папки назначения, отсутствующие в источнике
  --subscribe2         подписаться на созданные папки назначения
  --uidexpunge2        использовать UID EXPUNGE (RFC 4315) на host2
  --expungeaftereach   expunge после каждого удаления (по умолчанию)
  --noexpungeaftereach отключить expunge после каждого удаления
  --skipemptyfolders   пустые папки host1 не создаются на host2 (по умолчанию)
  --noskipemptyfolders создавать пустые папки host1 на host2
  --skipcrossduplicates не копировать сообщение, если ключ уже есть в любой папке host2
  --noresyncflags      не пересинхронизировать флаги
  --syncflagsaftercopy синхронизировать флаги сразу после APPEND
  --filterflags        фильтровать нестандартные системные флаги (по умолчанию)
  --nofilterflags      отключить фильтрацию флагов
  --regexflag S        Perl-подобная замена флагов s/from/to/ (повторяется)
  --syncinternaldates  сохранять INTERNALDATE источника (по умолчанию)
  --nosyncinternaldates использовать текущую дату при APPEND
  --minsize/--maxsize  фильтр по размеру сообщения (байт)
  --minage/--maxage    фильтр по возрасту сообщения (дней)
  --appendlimit N      пропускать сообщения больше N байт
  --truncmess N        усекать сообщения до N байт
  --skipmess RE        пропустить сообщения, содержимое которых совпадает с RE (повторяется)
  --maxmessagespersecond N  ограничить скорость: сообщений в секунду
  --maxbytespersecond  N  ограничить скорость: байт в секунду
  --maxbytesafter N    байты, после которых учитывается --maxbytespersecond
  --maxsleep N         максимальная пауза троттлинга в секундах (по умолчанию 2.0)
  --errorsmax N        остановить при достижении N ошибок (по умолчанию 50)
  --dry                имитация без записи
  --dry1               dry-режим для host1 (по умолчанию при --dry)
  --nodry1             отключить dry1
  --logdir DIR         каталог для файлов журнала (по умолчанию LOG_imapsync)
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
