package options

import "testing"

func TestParseBasic(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--password1", "p1",
		"--host2", "h2", "--user2", "u2", "--password2", "p2",
		"--ssl1", "--ssl2", "--useheader", "Message-Id",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Host1 != "h1" || o.Host2 != "h2" {
		t.Fatalf("hosts: %+v", o)
	}
	if !o.SSL1 || !o.SSL2 {
		t.Fatalf("ssl flags not set")
	}
	// useheader добавляется к значениям по умолчанию.
	found := false
	for _, h := range o.UseHeader {
		if h == "Message-Id" {
			found = true
		}
	}
	if !found {
		t.Fatalf("useheader not appended: %v", o.UseHeader)
	}
}

func TestParseEquals(t *testing.T) {
	o, err := Parse([]string{
		"--host1=h1", "--user1=u1", "--host2=h2", "--user2=u2",
		"--port1=993", "--threads=4",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Host1 != "h1" || o.Port1 != 993 || o.Threads != 4 {
		t.Fatalf("equals parsing: %+v", o)
	}
}

// Порт 993 подразумевает imap-over-TLS (Perl: $SSL1 ||= $PORT1 == 993).
func TestPort993ImpliesSSL(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "imap.yandex.ru:993", "--user1", "u1",
		"--host2", "127.0.0.1:45175", "--user2", "u2", "--password2", "p2",
		"--ssl2", "--sslargs2", "SSL_verify_mode=0",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.SSL1 {
		t.Fatalf("host1 на 993 без --ssl1: ожидаем SSL1=true, got %+v", o)
	}
	if o.Port2 != 45175 || !o.SSL2 {
		t.Fatalf("host2: port=%d ssl=%v", o.Port2, o.SSL2)
	}
}

// Явные --nosslN/--sslN имеют приоритет над автоопределением по порту 993.
func TestPort993ExplicitSSLWins(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1:993", "--user1", "u1",
		"--host2", "h2:993", "--user2", "u2",
		"--nossl1", "--ssl2",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.SSL1 {
		t.Fatalf("--nossl1 на 993: ожидаем SSL1=false, got %+v", o)
	}
	if !o.SSL2 {
		t.Fatalf("--ssl2 на 993: ожидаем SSL2=true, got %+v", o)
	}
}

// Порт, заданный отдельным --portN, тоже включает TLS по умолчанию.
func TestPortFlag993ImpliesSSL(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1",
		"--host2", "h2", "--user2", "u2",
		"--port1", "993",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.SSL1 {
		t.Fatalf("--port1 993: ожидаем SSL1=true, got %+v", o)
	}
	if o.SSL2 {
		t.Fatalf("host2 без порта 993: ожидаем SSL2=false, got %+v", o)
	}
}

func TestDelete1ImpliesExpunge1(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--delete1",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.Delete1 || !o.Expunge1 {
		t.Fatalf("delete1 should imply expunge1: %+v", o)
	}
}

func TestF1F2(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--f1f2", "Src", "Dst",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(o.F1F2) != 1 || o.F1F2[0][0] != "Src" || o.F1F2[0][1] != "Dst" {
		t.Fatalf("f1f2: %+v", o.F1F2)
	}
}

func TestUnknownOption(t *testing.T) {
	_, err := Parse([]string{"--bogus"})
	if err == nil {
		t.Fatalf("expected error for unknown option")
	}
}

func TestValidate(t *testing.T) {
	o := New()
	if err := o.Validate(); err == nil {
		t.Fatalf("expected validation error for empty options")
	}
	o.Host1, o.User1, o.Host2, o.User2 = "a", "b", "c", "d"
	if err := o.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestErrorsMaxDefault(t *testing.T) {
	o := New()
	if o.ErrorsMax != 50 {
		t.Fatalf("errorsmax default should be 50, got %d", o.ErrorsMax)
	}
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--errorsmax", "100",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.ErrorsMax != 100 {
		t.Fatalf("errorsmax: %+v", o.ErrorsMax)
	}
	bad := New()
	bad.Host1, bad.User1, bad.Host2, bad.User2 = "a", "b", "c", "d"
	bad.ErrorsMax = 0
	if err := bad.Validate(); err == nil {
		t.Fatalf("expected validation error for errorsmax <= 0")
	}
}

func TestAuthUserImpliesPlain(t *testing.T) {
	// Как в imapsync: authmech ||= authuser ? 'PLAIN' : 'LOGIN'.
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--authuser1", "admin",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.AuthUser1 != "admin" {
		t.Fatalf("authuser1: %+v", o.AuthUser1)
	}
	if o.AuthMech1 != "PLAIN" {
		t.Fatalf("authmech1 should default to PLAIN with authuser1, got %q", o.AuthMech1)
	}
	// host2 без authuser остаётся LOGIN.
	if o.AuthMech2 != "LOGIN" {
		t.Fatalf("authmech2 should stay LOGIN, got %q", o.AuthMech2)
	}

	// Явно заданный authmech не переопределяется.
	o, err = Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--authmech1", "login", "--authuser1", "admin",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.AuthMech1 != "LOGIN" {
		t.Fatalf("explicit authmech1 must win, got %q", o.AuthMech1)
	}
}

func TestProxyAuthRequiresAuthUser(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--proxyauth1",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.ProxyAuth1 {
		t.Fatalf("proxyauth1 not set")
	}
	if err := o.Validate(); err == nil {
		t.Fatalf("expected --proxyauth1 to require --authuser1")
	}

	o, err = Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--proxyauth1", "--authuser1", "admin",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := o.Validate(); err != nil {
		t.Fatalf("proxyauth1 with authuser1 should validate: %v", err)
	}
}

func TestPrefix1Prefix2(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--prefix1", "INBOX.", "--prefix2=INBOX/",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Prefix1 != "INBOX." || o.Prefix2 != "INBOX/" {
		t.Fatalf("prefix1/prefix2: %q %q", o.Prefix1, o.Prefix2)
	}
}

func TestSkipMess(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--skipmess", "X-Spam-Flag: YES", "--skipmess", "^From:.*spam",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(o.SkipMess) != 2 || len(o.SkipMessRe) != 2 {
		t.Fatalf("skipmess: %v", o.SkipMess)
	}
	if !o.SkipMessRe[0].MatchString("X-Spam-Flag: YES\r\n") {
		t.Fatalf("skipmess regex 0 should match")
	}
	if o.SkipMessRe[1].MatchString("From: ok@x.ru") {
		t.Fatalf("skipmess regex 1 should not match")
	}

	_, err = Parse([]string{"--skipmess", "(unclosed"})
	if err == nil {
		t.Fatalf("expected error for invalid skipmess regex")
	}
}

func TestThrottleOptions(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--maxmessagespersecond", "2", "--maxbytespersecond", "2000",
		"--maxbytesafter", "4000",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.MaxMessagesPerSecond != 2 {
		t.Fatalf("maxmessagespersecond: %v", o.MaxMessagesPerSecond)
	}
	if o.MaxBytesPerSecond != 2000 {
		t.Fatalf("maxbytespersecond: %v", o.MaxBytesPerSecond)
	}
	if o.MaxBytesAfter != 4000 {
		t.Fatalf("maxbytesafter: %v", o.MaxBytesAfter)
	}

	_, err = Parse([]string{"--maxmessagespersecond", "abc"})
	if err == nil {
		t.Fatalf("expected error for bad maxmessagespersecond")
	}
	_, err = Parse([]string{"--maxbytespersecond", "abc"})
	if err == nil {
		t.Fatalf("expected error for bad maxbytespersecond")
	}
}

func TestSepSearchSkipHeader(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--sep1", "/", "--sep2", ".",
		"--search1", "UNSEEN", "--search2", "FLAGGED",
		"--skipheader", "^X-",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Sep1 != "/" || o.Sep2 != "." {
		t.Fatalf("sep1/sep2: %q %q", o.Sep1, o.Sep2)
	}
	if o.Search1 != "UNSEEN" || o.Search2 != "FLAGGED" {
		t.Fatalf("search1/search2: %q %q", o.Search1, o.Search2)
	}
	if o.SkipHeaderRe == nil || !o.SkipHeaderRe.MatchString("X-Spam") {
		t.Fatalf("skipheader regex not set correctly")
	}
}

func TestDry1Logdir(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--dry", "--logdir", "/tmp/logs",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.Dry || !o.Dry1 {
		t.Fatalf("dry should imply dry1: %+v", o)
	}
	if o.Logdir != "/tmp/logs" {
		t.Fatalf("logdir: %q", o.Logdir)
	}
	// nodry1 отключает dry1.
	o, err = Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--dry", "--nodry1",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.Dry || o.Dry1 {
		t.Fatalf("nodry1 should disable dry1: %+v", o)
	}
}

func TestUseUIDUseCache(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--useuid", "--cachedir", "/tmp/cache",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.UseUID {
		t.Fatalf("useuid not set")
	}
	// useuid подразумевает usecache.
	if !o.UseCache {
		t.Fatalf("useuid should imply usecache")
	}
	if o.CacheDir != "/tmp/cache" {
		t.Fatalf("cachedir: %q", o.CacheDir)
	}
}

func TestUseCacheSkipCrossConflict(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--usecache", "--skipcrossduplicates",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := o.Validate(); err == nil {
		t.Fatalf("expected conflict between usecache and skipcrossduplicates")
	}
}

func TestRegexTrans2IncludeExclude(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--regextrans2", "s/^INBOX\\./New./",
		"--include", "^INBOX/Work",
		"--exclude", "^INBOX/Junk",
		"--folderfirst", "INBOX",
		"--folderlast", "Trash",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(o.RegexTrans2) != 1 {
		t.Fatalf("regextrans2: %v", o.RegexTrans2)
	}
	if len(o.IncludeRe) != 1 {
		t.Fatalf("include: %v", o.Include)
	}
	if len(o.ExcludeRe) != 1 {
		t.Fatalf("exclude: %v", o.Exclude)
	}
	if len(o.FolderFirst) != 1 || o.FolderFirst[0] != "INBOX" {
		t.Fatalf("folderfirst: %v", o.FolderFirst)
	}
	if len(o.FolderLast) != 1 || o.FolderLast[0] != "Trash" {
		t.Fatalf("folderlast: %v", o.FolderLast)
	}
}

func TestSyncInternalDatesFilterFlags(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--nosyncinternaldates", "--nofilterflags",
		"--regexflag", "s/\\*\\$/",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.SyncInternalDates {
		t.Fatalf("nosyncinternaldates should disable syncinternaldates")
	}
	if o.FilterFlags {
		t.Fatalf("nofilterflags should disable filterflags")
	}
	if len(o.RegexFlag) != 1 {
		t.Fatalf("regexflag: %v", o.RegexFlag)
	}
}

func TestAppendlimitTruncmess(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--appendlimit", "1000000", "--truncmess", "500000",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.Appendlimit != 1000000 {
		t.Fatalf("appendlimit: %d", o.Appendlimit)
	}
	if o.Truncmess != 500000 {
		t.Fatalf("truncmess: %d", o.Truncmess)
	}
}

func TestMaxSleep(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--maxsleep", "5.5",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.MaxSleep != 5.5 {
		t.Fatalf("maxsleep: %v", o.MaxSleep)
	}
}

func TestDelete2ImpliesUidExpunge2(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--delete2",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.UidExpunge2 {
		t.Fatalf("delete2 should imply uidexpunge2")
	}
}

func TestDelete2DuplicatesImpliesUidExpunge2(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--delete2duplicates",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.UidExpunge2 {
		t.Fatalf("delete2duplicates should imply uidexpunge2")
	}
}

func TestSyncFlagsAfterCopy(t *testing.T) {
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--syncflagsaftercopy",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !o.SyncFlagsAfterCopy {
		t.Fatalf("syncflagsaftercopy not set")
	}
}

func TestExpungeAfterEachDefault(t *testing.T) {
	o := New()
	if !o.ExpungeAfterEach {
		t.Fatalf("expungeaftereach should default to true")
	}
	o, err := Parse([]string{
		"--host1", "h1", "--user1", "u1", "--host2", "h2", "--user2", "u2",
		"--noexpungeaftereach",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if o.ExpungeAfterEach {
		t.Fatalf("noexpungeaftereach should disable expungeaftereach")
	}
}
