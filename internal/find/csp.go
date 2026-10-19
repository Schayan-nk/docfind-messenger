package find

// The Content-Security-Policy, and how the pages earn a strict one.
//
// The policy used to carry 'unsafe-inline' for scripts, for an honest reason:
// these pages ship as embedded bytes with their script inline and no build
// step, so nothing else would have run them. The comment beside it said nonces
// were the natural next step. They are not, quite -- a nonce has to change on
// every response, and these pages are served with an ETag computed from their
// own bytes, so a per-response nonce would make every page body unique and
// every conditional request a full download. The point of the ETag is that a
// page costs a 304 until a new build lands.
//
// Hashes have neither problem. The script is fixed at compile time, so its
// hash is too: the body stays byte-identical, the ETag keeps working, and the
// policy names exactly the scripts this binary shipped. Anything injected
// later -- which is the whole threat -- hashes to something else and does not
// run.
//
// This is only affordable because no page uses an inline event handler. A
// single onclick="" would need 'unsafe-hashes' or a rewrite to
// addEventListener; all nine pages were already written the second way.
// TestNoPageUsesInlineEventHandlers keeps that true.
//
// style-src keeps 'unsafe-inline'. Inline style ATTRIBUTES cannot be hashed at
// all, the pages use them throughout, and the exposure is not comparable:
// injected CSS can restyle a page, injected script can act as the
// administrator reading it.
const (
	cspCommon = "default-src 'self'; " +
		"style-src 'self' 'unsafe-inline'; " +
		// data: and blob: are how the document preview shows embedded images.
		"img-src 'self' data: blob:; " +
		// blob: is the messenger previewing a video or recording before it
		// is uploaded; the page makes those addresses itself.
		"media-src 'self' blob:; " +
		"font-src 'self' data:; " +
		"connect-src 'self'; " +
		"object-src 'none'; " +
		"base-uri 'none'; " +
		"form-action 'self'; " +
		"frame-ancestors 'none'"

	// The policy for everything that is not one of the embedded pages: the
	// stylesheet, the icons, the vendored scripts, a document being
	// downloaded. None of them carries inline script, so none of them needs
	// any relaxation and the strictest script-src applies. This one is safe to
	// keep strict precisely because nothing it covers has a script to block.
	cspDefault = "script-src 'self'; " + cspCommon

	// The policy for the embedded pages, which carry their script inline.
	//
	// 'unsafe-inline' is a real weakness and it is here deliberately: see
	// pageCSP for what replacing it with hashes did to the live site, and for
	// the meta-tag approach that can bring them back safely.
	cspInline = "script-src 'self' 'unsafe-inline'; " + cspCommon
)

// pageCSP is the policy for one embedded page.
//
// REVERTED to 'unsafe-inline', and the reason is worth the paragraph, because
// the hash version was better security and still had to go.
//
// A hash policy authorises the exact bytes the server hashed. If anything
// between this process and the browser alters the HTML -- a proxy, a cache
// serving an older copy, a middlebox rewriting markup -- the delivered script
// no longer matches, and the browser refuses to run it. Not degrades: refuses.
// The page then renders its static placeholder and stops, with no error in the
// console, because a blocked script cannot report that it was blocked.
//
// That is exactly what happened on the live server. The reports page sat on
// "در حال بارگذاری گزارش‌ها…" for every user: the document the browser parsed
// carried a 200,391-byte script and the policy authorised the hash of a
// 204,792-byte one, so the page's whole application never started. Measured
// with a securitypolicyviolation listener on the live document, which reported
// script-src-elem blocking "inline".
//
// The lesson is about failure modes, not about hashes. A hardening change whose
// failure mode is a blank page for everyone is worse than the weakness it
// removes, and it fails in production on the one thing a test environment does
// not have: whatever sits in front of it.
//
// The way to keep the hashes is to send the policy INSIDE the document, in a
// <meta http-equiv>, so the policy and the bytes it describes can never
// separate -- a stale document then carries its own matching policy. That is
// the right fix and it is not this one, because the site is down and this
// change is the one that can be reasoned about completely.
func pageCSP(page []byte) string {
	return cspInline
}
