// Package imapx — обёртка над github.com/emersion/go-imap/client с
// подключением (TLS/STARTTLS), аутентификацией (LOGIN/XOAUTH2) и
// высокоуровневыми хелперами для синхронизации папок и сообщений.
package imapx

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/commands"
	"github.com/emersion/go-imap/responses"
	"github.com/emersion/go-imap/utf7"
	"github.com/emersion/go-sasl"
)

// asyncLiteralLimit — порог go-imap: литералы не длиннее передаются
// несинхронно ({N+}) при объявленном LITERAL+, длиннее — синхронно с
// ожиданием continuation-запроса (см. AppendMessage).
const asyncLiteralLimit = 4096

// Conn — активное IMAP-соединение с метаданными.
type Conn struct {
	Client   *client.Client
	Host     string
	Port     int
	User     string
	IsTLS    bool
	Caps     map[string]bool
	ReadOnly bool
}

// XOAuth2Client реализует SASL-механизм XOAUTH2 (Gmail/MS).
type XOAuth2Client struct {
	User  string
	Token string
}

// Start формирует initial response SASL-механизма XOAUTH2 (RFC 7628 / Gmail):
//
//	user=<user>\x01auth=Bearer <token>\x01\x01
//
// ВАЖНО: ir — СЫРЫЕ байты. Контракт go-sasl/go-imap: base64 применяет
// go-imap (commands/authenticate.go), а не клиент. Двойное кодирование
// давало серверу base64-строку вместо SASL-строк и Яндекс отвечал
// «AUTHENTICATE internal server error» (Яндекс не объявляет SASL-IR,
// поэтому go-imap отправляет ir ответом на «+» — тот же путь).
func (c *XOAuth2Client) Start() (mech string, ir []byte, err error) {
	mech = "XOAUTH2"
	ir = []byte("user=" + c.User + "\x01auth=Bearer " + c.Token + "\x01\x01")
	return
}

func (c *XOAuth2Client) Next(challenge []byte) ([]byte, error) {
	// Отвечаем пустой строкой, чтобы корректно завершить обмен.
	return []byte(""), nil
}

// bytesLiteral реализует imap.Literal для []byte.
type bytesLiteral struct {
	*bytes.Reader
}

func (b bytesLiteral) Len() int { return b.Reader.Len() }

// Connect устанавливает соединение с учётом ssl/tls/insecure/timeout.
func Connect(host string, port int, ssl, tlsStart, insecure bool, timeout time.Duration, debugW io.Writer) (*Conn, error) {
	addr := net.JoinHostPort(host, fmt.Sprint(port))
	tlsCfg := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: insecure,
	}

	var c *client.Client
	var err error

	if ssl {
		c, err = client.DialTLS(addr, tlsCfg)
	} else {
		c, err = client.Dial(addr)
	}
	if err != nil {
		return nil, fmt.Errorf("подключение к %s: %w", addr, err)
	}

	if timeout > 0 {
		c.Timeout = timeout
	}
	if debugW != nil {
		c.SetDebug(debugW)
	}

	conn := &Conn{Client: c, Host: host, Port: port, IsTLS: ssl}

	if caps, cerr := c.Capability(); cerr == nil {
		conn.Caps = caps
	}

	if !ssl && tlsStart {
		if ok, _ := c.SupportStartTLS(); !ok {
			c.Logout()
			return nil, fmt.Errorf("сервер %s не поддерживает STARTTLS", host)
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			c.Logout()
			return nil, fmt.Errorf("STARTTLS к %s: %w", host, err)
		}
		conn.IsTLS = true
		if caps, cerr := c.Capability(); cerr == nil {
			conn.Caps = caps
		}
	}

	return conn, nil
}

// xoauth2Response — handler обмена XOAUTH2 при immediate response.
//
// После `AUTHENTICATE XOAUTH2 <ir>` сервер либо отвечает tagged-статусом
// (OK/NO), либо (Gmail при отказе) шлёт challenge с JSON-описанием —
// по спецификации Gmail клиент обязан ответить пустой строкой, после
// чего приходит NO.
type xoauth2Response struct {
	replies chan []byte
}

func (r *xoauth2Response) Replies() <-chan []byte { return r.replies }

func (r *xoauth2Response) Handle(resp imap.Resp) error {
	if _, ok := resp.(*imap.ContinuationReq); !ok {
		return responses.ErrUnhandled
	}
	// Пустой ответ на challenge (Gmail XOAUTH2).
	r.replies <- []byte("\r\n")
	return nil
}

// xoauth2Immediate выполняет AUTHENTICATE XOAUTH2 с immediate response —
// initial response передаётся АРГУМЕНТОМ команды (RFC 4959 §3):
//
//	<tag> AUTHENTICATE XOAUTH2 base64(user=...\x01auth=Bearer <tok>\x01\x01)
//
// go-imap умеет immediate response только когда сервер объявил SASL-IR.
// Серверы без SASL-IR (Яндекс) на двухшаговый обмен
// «AUTHENTICATE XOAUTH2» → «+» → ответ отвечают
// «NO [UNAVAILABLE] AUTHENTICATE internal server error», поэтому обмен
// ведётся вручную через client.Execute.
func (c *Conn) xoauth2Immediate(user, token string) error {
	ir := []byte("user=" + user + "\x01auth=Bearer " + token + "\x01\x01")
	cmd := &commands.Authenticate{Mechanism: "XOAUTH2", InitialResponse: ir}
	res := &xoauth2Response{replies: make(chan []byte, 4)}

	status, err := c.Client.Execute(cmd, res)
	if err != nil {
		return err
	}
	if err := status.Err(); err != nil {
		return err
	}
	c.Client.SetState(imap.AuthenticatedState, c.Client.Mailbox())
	return nil
}

// Login выполняет аутентификацию выбранным механизмом.
//
// authUser/proxyAuth соответствуют --authuserN/--proxyauthN в imapsync:
//
//	--proxyauthN: логинимся под authUser (LOGIN), затем отправляем
//	PROXYAUTH <user> (Cyrus-style proxy authentication);
//	PLAIN с authUser: SASL PLAIN с authorization id = user (переносимый
//	ящик) и authentication id = authUser (admin), т.е. на проводе
//	"user\x00authUser\x00password" — ровно как imapsync (plainauth):
//
//	mysprintf("%s\x00%s\x00%s", $imap->User,
//	          defined $imap->Authuser ? $imap->Authuser : "",
//	          $imap->Password)
//
// Это важно для Dovecot master users / Zimbra admin proxy: сервер
// проверяет, что authentication id (admin) имеет право на authorization
// id (ящик), а не наоборот.
func (c *Conn) Login(user, password, mech, authUser string, proxyAuth bool) error {
	c.User = user

	// proxyauth: аутентификация под admin-пользователем, затем PROXYAUTH.
	if proxyAuth {
		if authUser == "" {
			return fmt.Errorf("proxyauth требует authuser")
		}
		if err := c.Client.Login(authUser, password); err != nil {
			return fmt.Errorf("логин %s@%s (proxyauth под %s): %w", user, c.Host, authUser, err)
		}
		if err := c.ProxyAuth(user); err != nil {
			return fmt.Errorf("PROXYAUTH %s@%s под %s: %w", user, c.Host, authUser, err)
		}
		return nil
	}

	switch strings.ToUpper(mech) {
	case "XOAUTH2":
		// SASL-IR объявлен → штатный обмен go-imap (initial response
		// отправляется ответом на «+», сервер это понимает).
		if saslIR, _ := c.Client.Support("SASL-IR"); saslIR {
			xo := &XOAuth2Client{User: user, Token: password}
			if err := c.Client.Authenticate(xo); err != nil {
				return fmt.Errorf("XOAUTH2-логин %s@%s: %w", user, c.Host, err)
			}
			return nil
		}
		// Без SASL-IR (Яндекс): immediate response аргументом команды.
		if err := c.xoauth2Immediate(user, password); err != nil {
			return fmt.Errorf("XOAUTH2-логин %s@%s: %w", user, c.Host, err)
		}
		return nil
	case "OAUTHBEARER":
		oc := sasl.NewOAuthBearerClient(&sasl.OAuthBearerOptions{
			Username: user,
			Host:     c.Host,
			Port:     c.Port,
			Token:    password,
		})
		if err := c.Client.Authenticate(oc); err != nil {
			return fmt.Errorf("OAUTHBEARER-логин %s@%s: %w", user, c.Host, err)
		}
		return nil
	case "PLAIN":
		// SASL PLAIN в стиле imapsync (plainauth):
		// authzid = user (переносимый ящик), authcid = authUser (admin).
		// На проводе: base64("user\x00authUser\x00password").
		// Без authUser получается обычный PLAIN ("user\x00\x00password").
		pc := sasl.NewPlainClient(user, authUser, password)
		if err := c.Client.Authenticate(pc); err != nil {
			return fmt.Errorf("PLAIN-логин %s@%s (authuser %q): %w", user, c.Host, authUser, err)
		}
		return nil
	default: // LOGIN
		if err := c.Client.Login(user, password); err != nil {
			return fmt.Errorf("логин %s@%s: %w", user, c.Host, err)
		}
		return nil
	}
}

// ProxyAuth отправляет команду PROXYAUTH <user> (Cyrus IMAP).
// Используется после логина под admin-пользователем (--proxyauthN).
func (c *Conn) ProxyAuth(user string) error {
	cmd := &imap.Command{
		Name:      "PROXYAUTH",
		Arguments: []interface{}{user},
	}
	status, err := c.Client.Execute(cmd, nil)
	if err != nil {
		return err
	}
	if err := status.Err(); err != nil {
		return err
	}
	return nil
}

// Logout корректно закрывает соединение.
func (c *Conn) Logout() {
	if c.Client != nil {
		_ = c.Client.Logout()
	}
}

// ListFolders возвращает список папок (LIST "" "*").
func (c *Conn) ListFolders() ([]*imap.MailboxInfo, error) {
	ch := make(chan *imap.MailboxInfo, 64)
	go func() {
		_ = c.Client.List("", "*", ch)
	}()
	var out []*imap.MailboxInfo
	for m := range ch {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delimiter возвращает разделитель путей (из LIST), по умолчанию "/".
func (c *Conn) Delimiter(folders []*imap.MailboxInfo) string {
	for _, f := range folders {
		if f.Delimiter != "" {
			return f.Delimiter
		}
	}
	return "/"
}

// Status получает MESSAGES/UIDVALIDITY/UNSEEN для папки без её открытия.
func (c *Conn) Status(name string) (*imap.MailboxStatus, error) {
	items := []imap.StatusItem{
		imap.StatusMessages, imap.StatusUidValidity, imap.StatusUnseen,
	}
	return c.Client.Status(name, items)
}

// Select открывает папку (readWrite=true для записи).
func (c *Conn) Select(name string, readWrite bool) (*imap.MailboxStatus, error) {
	st, err := c.Client.Select(name, !readWrite)
	if err != nil {
		return nil, fmt.Errorf("SELECT %q: %w", name, err)
	}
	c.ReadOnly = !readWrite
	return st, nil
}

// Create создаёт папку.
func (c *Conn) Create(name string) error {
	return c.Client.Create(name)
}

// Subscribe подписывается на папку.
func (c *Conn) Subscribe(name string) error {
	return c.Client.Subscribe(name)
}

// DeleteFolder удаляет папку.
func (c *Conn) DeleteFolder(name string) error {
	return c.Client.Delete(name)
}

// UidSearchAll возвращает все UID в текущей открытой папке с учётом критерия (если задан).
func (c *Conn) UidSearchAll(criteriaStr string) ([]uint32, error) {
	crit := imap.NewSearchCriteria()
	if criteriaStr != "" {
		parts := strings.Fields(criteriaStr)
		for _, p := range parts {
			up := strings.ToUpper(p)
			switch up {
			case "ALL":
				// по умолчанию
			case "SEEN":
				crit.WithFlags = append(crit.WithFlags, imap.SeenFlag)
			case "UNSEEN":
				crit.WithoutFlags = append(crit.WithoutFlags, imap.SeenFlag)
			case "FLAGGED":
				crit.WithFlags = append(crit.WithFlags, imap.FlaggedFlag)
			case "UNFLAGGED":
				crit.WithoutFlags = append(crit.WithoutFlags, imap.FlaggedFlag)
			case "ANSWERED":
				crit.WithFlags = append(crit.WithFlags, imap.AnsweredFlag)
			case "UNANSWERED":
				crit.WithoutFlags = append(crit.WithoutFlags, imap.AnsweredFlag)
			case "DRAFT":
				crit.WithFlags = append(crit.WithFlags, imap.DraftFlag)
			case "UNDRAFT":
				crit.WithoutFlags = append(crit.WithoutFlags, imap.DraftFlag)
			case "DELETED":
				crit.WithFlags = append(crit.WithFlags, imap.DeletedFlag)
			case "UNDELETED":
				crit.WithoutFlags = append(crit.WithoutFlags, imap.DeletedFlag)
			}
		}
	}
	return c.Client.UidSearch(crit)
}

// UidExpunge выполняет UID EXPUNGE для списка UID (RFC 4315), если поддерживается сервером.
func (c *Conn) UidExpunge(uids []uint32) error {
	if len(uids) == 0 {
		return nil
	}
	set := &imap.SeqSet{}
	set.AddNum(uids...)
	cmd := &imap.Command{
		Name:      "UID EXPUNGE",
		Arguments: []interface{}{set},
	}
	status, err := c.Client.Execute(cmd, nil)
	if err != nil {
		return err
	}
	return status.Err()
}

// UidSearchByHeader ищет сообщения, содержащие указанный заголовок
// (SEARCH HEADER <name> ""), и возвращает UID.
func (c *Conn) UidSearchByHeader(name string) ([]uint32, error) {
	crit := imap.NewSearchCriteria()
	crit.Header = make(textproto.MIMEHeader)
	crit.Header.Add(name, "")
	return c.Client.UidSearch(crit)
}

// FetchHeaders извлекает указанные заголовки (и FLAGS/INTERNALDATE/RFC822.SIZE)
// для набора UID. Возвращает map[uid]*imap.Message.
func (c *Conn) FetchHeaders(uids []uint32, headers []string) (map[uint32]*imap.Message, error) {
	if len(uids) == 0 {
		return map[uint32]*imap.Message{}, nil
	}
	set := &imap.SeqSet{}
	set.AddNum(uids...)

	items := []imap.FetchItem{
		imap.FetchUid, imap.FetchFlags, imap.FetchInternalDate, imap.FetchRFC822Size,
	}
	if len(headers) > 0 {
		items = append(items, headerFieldsItem(headers))
	}

	ch := make(chan *imap.Message, 64)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Client.UidFetch(set, items, ch)
	}()

	out := map[uint32]*imap.Message{}
	for m := range ch {
		out[m.Uid] = m
	}
	if fetchErr := <-errCh; fetchErr != nil {
		return out, fetchErr
	}
	return out, nil
}

// headerFieldsItem формирует BODY.PEEK[HEADER.FIELDS (A B C)].
func headerFieldsItem(headers []string) imap.FetchItem {
	return imap.FetchItem("BODY.PEEK[HEADER.FIELDS (" + strings.Join(headers, " ") + ")]")
}

// HeaderInfo — извлечённые заголовки и метаданные одного сообщения.
type HeaderInfo struct {
	Values map[string][]string // canonical header name -> values
	Flags  []string
	Date   time.Time
	Size   uint32
}

// FetchHeaderMap пакетно извлекает заголовки и метаданные (FLAGS,
// INTERNALDATE, RFC822.SIZE) для набора UID. Возвращает map[uid]HeaderInfo.
func (c *Conn) FetchHeaderMap(uids []uint32, headers []string) (map[uint32]*HeaderInfo, error) {
	if len(uids) == 0 {
		return map[uint32]*HeaderInfo{}, nil
	}
	set := &imap.SeqSet{}
	set.AddNum(uids...)

	items := []imap.FetchItem{
		imap.FetchUid, imap.FetchFlags, imap.FetchInternalDate, imap.FetchRFC822Size,
	}
	section := HeaderSection(headers)
	if section != nil {
		items = append(items, section.FetchItem())
	}

	ch := make(chan *imap.Message, 128)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Client.UidFetch(set, items, ch)
	}()

	out := map[uint32]*HeaderInfo{}
	for m := range ch {
		info := &HeaderInfo{
			Flags:  m.Flags,
			Date:   m.InternalDate,
			Size:   m.Size,
			Values: map[string][]string{},
		}
		if section != nil {
			if lit := m.GetBody(section); lit != nil {
				data, rerr := io.ReadAll(lit)
				if rerr == nil {
					if vals, perr := ParseHeaderValues(data); perr == nil {
						info.Values = vals
					}
				}
			}
		}
		out[m.Uid] = info
	}
	if fetchErr := <-errCh; fetchErr != nil {
		return out, fetchErr
	}
	return out, nil
}

// HeaderSection возвращает BodySectionName для BODY.PEEK[HEADER.FIELDS (...)]
// по списку заголовков. Используется для извлечения значений заголовков.
func HeaderSection(headers []string) *imap.BodySectionName {
	section, err := imap.ParseBodySectionName(headerFieldsItem(headers))
	if err != nil {
		return nil
	}
	return section
}

// ParseHeaderValues разбирает блок заголовков (как в начале сообщения) и
// возвращает карту canonical-имя заголовка -> список значений.
func ParseHeaderValues(data []byte) (map[string][]string, error) {
	rd := textproto.NewReader(bufio.NewReader(bytes.NewReader(data)))
	hdr, err := rd.ReadMIMEHeader()
	if err != nil && len(hdr) == 0 {
		return nil, err
	}
	out := map[string][]string{}
	for k, v := range hdr {
		out[http.CanonicalHeaderKey(k)] = v
	}
	return out, nil
}

// FetchRfc822 извлекает полное сообщение (BODY.PEEK[]) для одного UID.
func (c *Conn) FetchRfc822(uid uint32) ([]byte, *imap.Message, error) {
	set := &imap.SeqSet{}
	set.AddNum(uid)
	items := []imap.FetchItem{imap.FetchUid, imap.FetchFlags, imap.FetchInternalDate, imap.FetchRFC822Size}
	section := &imap.BodySectionName{Peek: true}
	items = append(items, section.FetchItem())

	ch := make(chan *imap.Message, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Client.UidFetch(set, items, ch)
	}()

	var msg *imap.Message
	for m := range ch {
		msg = m
	}
	if fetchErr := <-errCh; fetchErr != nil {
		return nil, nil, fetchErr
	}
	if msg == nil {
		return nil, nil, fmt.Errorf("UID %d не найден", uid)
	}
	lit := msg.GetBody(section)
	if lit == nil {
		return nil, nil, fmt.Errorf("нет тела для UID %d", uid)
	}
	data, err := io.ReadAll(lit)
	if err != nil {
		return nil, nil, err
	}
	return data, msg, nil
}

// AppendMessage добавляет сообщение в папку с флагами и датой.
//
// Литералы длиннее asyncLiteralLimit go-imap отправляет синхронно —
// с ожиданием continuation-запроса «+». Ряд серверов (example.com)
// continuation не присылает, и запись прерывается ошибкой
// «cannot send literal: no continuation request received». Поэтому
// для больших писем используется APPEND с несинхронным литералом
// ({N+}, RFC 3501 §2.5.10), если сервер объявил LITERAL+.
func (c *Conn) AppendMessage(folder string, flags []string, date time.Time, data []byte) error {
	lit := bytesLiteral{Reader: bytes.NewReader(data)}
	if len(data) > asyncLiteralLimit {
		if ok, _ := c.Client.Support("LITERAL+"); ok {
			if err := c.appendAsyncLiteral(folder, flags, date, data); err == nil {
				return nil
			} else if !isNoContinuationErr(err) {
				return err
			}
		}
	}
	return c.Client.Append(folder, flags, date, lit)
}

// appendAsyncLiteral выполняет APPEND с несинхронным литералом ({N+},
// RFC 3501 §2.5.10). Тело письма передаётся как RawString — go-imap
// пишет его в поток как есть, без ожидания continuation-запроса «+»,
// который серверы вроде example.com не присылают.
func (c *Conn) appendAsyncLiteral(folder string, flags []string, date time.Time, data []byte) error {
	name, err := utf7.Encoding.NewEncoder().String(folder)
	if err != nil {
		name = folder
	}

	args := []interface{}{imap.FormatMailboxName(name)}
	if len(flags) > 0 {
		list := make([]interface{}, len(flags))
		for i, f := range flags {
			list[i] = imap.RawString(f)
		}
		args = append(args, list)
	}
	if !date.IsZero() {
		args = append(args, imap.DateTime(date))
	}
	// Заголовок литерала + тело одним полем: writeFields запишет его
	// как есть, writeCrlf добавит завершающий CRLF команды.
	args = append(args, imap.RawString(fmt.Sprintf("{%d+}\r\n", len(data))+string(data)))

	status, err := c.Client.Execute(&imap.Command{Name: "APPEND", Arguments: args}, nil)
	if err != nil {
		return err
	}
	return status.Err()
}

// isNoContinuationErr — ошибка go-imap об отсутствии continuation-запроса.
func isNoContinuationErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no continuation request received")
}

// toInterfaces преобразует срез строк флагов в []interface{} для go-imap client.UidStore.
func toInterfaces(flags []string) []interface{} {
	out := make([]interface{}, len(flags))
	for i, f := range flags {
		out[i] = f
	}
	return out
}

// UidStoreFlags устанавливает флаги (FLAGS, silent) для набора UID.
func (c *Conn) UidStoreFlags(uids []uint32, flags []string) error {
	set := &imap.SeqSet{}
	set.AddNum(uids...)
	return c.Client.UidStore(set, imap.FormatFlagsOp(imap.SetFlags, true), toInterfaces(flags), nil)
}

// UidDelete помечает сообщения как удалённые (\Deleted).
func (c *Conn) UidDelete(uids []uint32) error {
	set := &imap.SeqSet{}
	set.AddNum(uids...)
	return c.Client.UidStore(set, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.DeletedFlag}, nil)
}

// Expunge удаляет помеченные сообщения из текущей папки.
func (c *Conn) Expunge() error {
	ch := make(chan uint32, 64)
	go func() {
		_ = c.Client.Expunge(ch)
	}()
	for range ch {
	}
	return nil
}

// UidCopy копирует сообщения (по UID) в другую папку на том же сервере.
func (c *Conn) UidCopy(uids []uint32, dest string) error {
	set := &imap.SeqSet{}
	set.AddNum(uids...)
	return c.Client.UidCopy(set, dest)
}
