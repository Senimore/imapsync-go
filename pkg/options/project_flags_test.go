package options

import (
	"testing"
)

// projectArgs — аргументы ровно в той форме, которую генерирует
// планировщика внешнего планировщика (строковые флаги со значением,
// булевые — без значения, слайсы — повтором).
var projectArgs = []string{
	"--host1", "mail.example.com:143",
	"--host2", "imap.gmail.com",
	"--user1", "user1@example.com",
	"--user2", "user2@gmail.com",
	"--password1", "secret1",
	"--password2", "secret2",
	"--authuser1", "admin@example.com",
	"--authuser2", "admin@gmail.com",
	"--authmech1", "PLAIN",
	"--authmech2", "PLAIN",
	"--proxyauth1", "true",
	"--proxyauth2", "true",
	"--port1", "143",
	"--port2", "993",
	"--sep1", "/",
	"--sep2", "/",
	"--search1", "UTF8",
	"--search2", "UTF8",
	"--ssl1",
	"--ssl2",
	"--tls1",
	"--tls2",
	"--nossl1",
	"--notls1",
	"--compress1", "mysql",
	"--compress2", "zlib",
	"--useheader", "Message-Id",
	"--userheader", "Date",
	"--skipheader", "^Received",
	"--useuid",
	"--usecache",
	"--cachedir", "/tmp/imapsync-cache",
	"--folder", "INBOX",
	"--folderrec", "^INBOX$",
	"--subfolder1", "x",
	"--subfolder2", "y",
	"--f1f2", "INBOX=INBOX2",
	"--automap",
	"--exclude1", "^Trash$",
	"--exclude", "^Junk$",
	"--exclude-all",
	"--prefix1", "",
	"--prefix2", "",
	"--regextrans2", "s,[ ]+,_,g",
	"--include", "^INBOX$",
	"--exclude", "^Drafts$",
	"--folderfirst", "INBOX",
	"--folderlast", "[Gmail]/All Mail",
	"--delete1",
	"--expunge1",
	"--delete2",
	"--expunge2",
	"--delete2folders",
	"--delete1emptyfolders",
	"--subscribe2",
	"--uidexpunge2",
	"--nouidexpunge2",
	"--expungeaftereach",
	"--noexpungeaftereach",
	"--skipcrossduplicates",
	"--delete2duplicates",
	"--noresyncflags",
	"--syncflagsaftercopy",
	"--filterflags",
	"--nofilterflags",
	"--regexflag", "s/\\\\Flagged//g",
	"--syncinternaldates",
	"--nosyncinternaldates",
	"--minsize", "0",
	"--maxsize", "10000000",
	"--minage", "0",
	"--maxage", "0",
	"--search", "ALL",
	"--skipmess", "X-NO-SUCH",
	"--maxmessagespersecond", "4",
	"--maxbytespersecond", "300000",
	"--maxbytesafter", "3000000000",
	"--maxsleep", "100",
	"--appendlimit", "3600000",
	"--truncmess", "10000000",
	"--errorsmax", "20",
	"--dry",
	"--dry1",
	"--nodry1",
	"--justconnect",
	"--justlogin",
	"--justfolders",
	"--justfoldersizes",
	"--justbanner",
	"--logfile", "/tmp/imapsync.log",
	"--logdir", "/tmp/imapsync-logs",
	"--nolog",
	"--debug",
	"--debugimap",
	"--threads", "1",
	"--exitwhenover", "100000000",
	"--addheader",
	"--skipemptyfolders",
	"--noskipemptyfolders",
	"--oauthaccesstoken1", "ya29.token",
	"--oauthaccesstoken2", "ya29.token2",
	"--oauthdirect1", "https://example.com/token",
	"--oauthdirect2", "https://example.com/token2",
	"--gmail1",
	"--gmail2",
	"--office1",
	"--office2",
	"--exchange1",
	"--exchange2",
	"--domino1",
	"--domino2",
	"--disarmreadreceipts",
	"--nodisarmreadreceipts",
	"--regexmess", "s,(.{10239}),$1\r\n,g",
	"--noregexmess",
	"--subcribed",
	"--subscribe",
	"--nosubscribe",
	"--subscribeall",
	"--nosyncacls",
	"--syncacls",
	"--synclabels",
	"--nosynclabels",
	"--resynclabels",
	"--noresynclabels",
	"--labels1",
	"--labels2",
	"--keepalive1",
	"--keepalive2",
	"--noabletosearch",
	"--noabletosearch1",
	"--noabletosearch2",
	"--buffersize", "131072",
	"--maxlinelength", "20000",
	"--syncduplicates",
	"--allowsizemismatch",
	"--skipsize",
	"--debugcrossduplicates",
	"--debugfolders",
	"--debugcontent",
	"--debugflags",
	"--debugssl", "4",
	"--debugssl1",
	"--debugssl2",
	"--debugimap1",
	"--debugimap2",
	"--noerrorsdump",
	"--nomodulesversion",
	"--no-modulesversion",
	"--modulesversion",
	"--passfile1", "/tmp/p1",
	"--passfile2", "/tmp/p2",
	"--domain1", "example.com",
	"--domain2", "gmail.com",
	"--authmd51",
	"--authmd52",
	"--showpasswords",
	"--nomixfolders",
	"--noid",
	"--nofoldersizes",
	"--nofoldersizesatend",
	"--pidfile", "/tmp/imapsync.pid",
	"--pidfilelocking",
	"--tmpdir", "/tmp",
	"--abort",
	"--emailreport1",
	"--emailreport2",
	"--noemailreport1",
	"--noemailreport2",
	"--pipemess", "cat",
	"--exitstatus0",
	"--tests",
	"--info",
	"--adtefromheader",
	"--usechache",
	"--nousechache",
	"--regtrans2", "s,foo,bar,",
	"--resyncflags",
	"--filterbuggyflags",
	"--delete2foldersonly", "/^Junk$/",
	"--delete2foldersbutnot", "/Tasks$/",
	"--noskipcrossduplicates",
	"--noexpunge2",
	"--noaddheader",
	"--nof1f2",
	"--noexclude",
	"--noinclude",
	"--nofolderlast",
	"--nosslargs",
	"--sslargs1", "SSL_verify_mode=0",
	"--sslargs2", "SSL_verify_mode=0",
	"--timeout1", "120",
	"--timeout2", "120",
	"--timeout", "120",
	"--authdirect1", "https://example.com/t",
	"--authdirect2", "https://example.com/t",
	"--nossl2",
	"--notls2",
	"--version",
}

func TestParseProjectArgs(t *testing.T) {
	o, err := Parse(projectArgs)
	if err != nil {
		t.Fatalf("разбор аргументов проекта завершился ошибкой: %v", err)
	}
	if o.Host1 != "mail.example.com" || o.Port1 != 143 {
		t.Errorf("host1/port1 = %q/%d, ожидаем mail.example.com/143", o.Host1, o.Port1)
	}
	if o.Host2 != "imap.gmail.com" {
		t.Errorf("host2 = %q", o.Host2)
	}
	if !o.ProxyAuth1 {
		t.Error("proxyauth1 не установлен")
	}
	if o.AuthMech1 != "XOAUTH2" {
		t.Errorf("authmech1 = %q, ожидаем XOAUTH2 (oauthaccesstoken1)", o.AuthMech1)
	}
	if o.OAuthAccessToken1 != "ya29.token" {
		t.Errorf("oauthaccesstoken1 = %q", o.OAuthAccessToken1)
	}
	if len(o.F1F2) == 0 || o.F1F2[0][0] != "INBOX" || o.F1F2[0][1] != "INBOX2" {
		t.Errorf("f1f2 = %v", o.F1F2)
	}
	if !o.Gmail1 || !o.Gmail2 {
		t.Error("gmail1/gmail2 не установлены")
	}
	if o.MaxBytesPerSecond != 300000 {
		t.Errorf("maxbytespersecond = %d", o.MaxBytesPerSecond)
	}
	if o.MaxSize != 10000000 {
		t.Errorf("maxsize = %d", o.MaxSize)
	}
	if o.Threads != 1 {
		t.Errorf("threads = %d", o.Threads)
	}
	if !o.NoLog {
		t.Error("nolog не установлен")
	}
	// --nossl1/--notls1 и --nossl2/--notls2 заданы явно: gmail-пресет не
	// должен их перебивать (семантика defined в Perl).
	if o.SSL1 || o.TLS1 {
		t.Errorf("ssl1/tls1 = %v/%v, ожидаем false при заданных --nossl1/--notls1", o.SSL1, o.TLS1)
	}
	if o.SSL2 || o.TLS2 {
		t.Errorf("ssl2/tls2 = %v/%v, ожидаем false при заданных --nossl2/--notls2", o.SSL2, o.TLS2)
	}
	if len(o.UseHeader) < 2 {
		t.Errorf("useheader = %v", o.UseHeader)
	}
	if o.SslArgs1 == nil || o.SslArgs1[0] != "SSL_verify_mode=0" {
		t.Errorf("sslargs1 = %v", o.SslArgs1)
	}
	// SSL_verify_mode=0 в --sslargsN => не проверять сертификат (как в Perl).
	if !o.SSLInsecure1() || !o.SSLInsecure2() {
		t.Error("SSLInsecure1/2 = false при SSL_verify_mode=0 в --sslargsN")
	}
	if o.Timeout1 != 120 {
		t.Errorf("timeout1 = %d", o.Timeout1)
	}
	if o.Delete2FoldersOnly != "/^Junk$/" {
		t.Errorf("delete2foldersonly = %q", o.Delete2FoldersOnly)
	}
	if !o.NoRegexMess || !o.NoF1F2 {
		t.Error("noregexmess/nof1f2 не установлены")
	}
	// --nodisarmreadreceipts принимается (no-op) — отдельного поля нет.
}
