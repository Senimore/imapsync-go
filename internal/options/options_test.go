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
