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
