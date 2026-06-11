package server

import (
	"net"
	"net/http"
	"net/url"
)

// IsLocalOriginOrAbsent は mutating endpoint の CSRF 対策ヘルパー:
//   - Origin / Referer ヘッダーが付いていなければ true（curl 等の non-browser）
//   - 付いていれば host が loopback / private (RFC 1918) / link-local のときだけ true
//   - "null" Origin（sandboxed iframe / data: / file:）は明示的に拒否
//
// Agentarium は既定で 127.0.0.1 bind だが、AGENTARIUM_ALLOW_PUBLIC=1 で LAN 公開も
// 許可される。public IP からの cross-site CSRF は弾きつつ、同 LAN 内
// (192.168.x.x / 10.x.x.x / 172.16-31.x.x / fe80::/10 / fc00::/7) からのアクセスは
// 許可する。browser は cross-site fetch で Origin を必ず付与するので、外部 public
// サイトからの誘導 POST は弾ける。
func IsLocalOriginOrAbsent(r *http.Request) bool {
	for _, raw := range []string{r.Header.Get("Origin"), r.Header.Get("Referer")} {
		if raw == "" {
			continue
		}
		if raw == "null" {
			return false
		}
		u, err := url.Parse(raw)
		if err != nil {
			return false
		}
		// 相対 path の Referer（例 "/foo"）は u.Hostname()=="" になり、
		// isTrustedNetworkHost("") は false を返すため拒否される（safe-side）。
		// browser は通常 Referer に絶対 URL を入れるので実害は無いが、
		// 将来 url.Parse 挙動が変わっても安全側に倒れることを明示しておく。
		if !isTrustedNetworkHost(u.Hostname()) {
			return false
		}
	}
	return true
}

// isTrustedNetworkHost は host が loopback / private (RFC 1918) / link-local の
// いずれかに該当するか判定する。詳細は IsLocalOriginOrAbsent のコメント参照。
func isTrustedNetworkHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// csrfGuard は mutating method (POST/PUT/PATCH/DELETE) に対して CSRF チェックを
// 行う middleware。GET/HEAD/OPTIONS は素通しする。
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			if !IsLocalOriginOrAbsent(r) {
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
