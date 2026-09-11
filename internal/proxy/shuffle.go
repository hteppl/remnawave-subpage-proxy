package proxy

import "net/http"

// maxShuffleBody caps what is buffered to rewrite; larger bodies stream through.
const maxShuffleBody = 8 << 20

func (p *Proxy) shuffles(info *requestInfo) bool {
	return p.shuffler.Enabled() && info.rq.ShortUUID != ""
}

// HTML, compressed or oversized bodies pass through.
func (p *Proxy) shuffle(resp *http.Response, info *requestInfo) {
	if !p.shuffles(info) || !rewritable(resp) {
		return
	}
	if isCompressed(resp.Header) {
		p.log.Debug("subscription arrived compressed, hosts left in place", "short_uuid", info.rq.ShortUUID)
		return
	}

	body, ok := drainBody(resp, maxShuffleBody)
	if !ok {
		return
	}
	if shuffled, changed := p.shuffler.Apply(body); changed {
		setBody(resp, shuffled)
		p.log.Debug("shuffled subscription hosts", "short_uuid", info.rq.ShortUUID, "client_type", info.rq.ClientType)
	}
}
