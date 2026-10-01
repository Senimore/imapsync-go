// Command imapsync-go — синхронизация двух IMAP-аккаунтов (аналог imapsync).
//
// Пример:
//
//	imapsync-go --host1 imap.src.ru --user1 a --password1 p1 \
//	            --host2 imap.dst.ru --user2 b --password2 p2 \
//	            --ssl1 --ssl2 --useheader Message-Id
//
// Вся логика вынесена в публичный пакет pkg/imapsync, чтобы модуль можно
// было использовать из сторонних проектов:
//
//	import "github.com/Senimore/imapsync-go/pkg/imapsync"
//	code := imapsync.Run(os.Args[1:], os.Stdout)
package main

import (
	"os"

	"github.com/Senimore/imapsync-go/pkg/imapsync"
)

func main() {
	os.Exit(imapsync.Run(os.Args[1:], os.Stdout))
}
