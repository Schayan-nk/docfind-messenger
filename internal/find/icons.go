package find

import "bytes"

// Icons come from one Lucide sprite, inlined into every page.
//
// The obvious form is <use href="/static/vendor/lucide/sprite.svg#search">,
// referencing the sprite as a file. That renders in Chromium -- verified in a
// browser -- but an external reference in <use> has never been supported by
// WebKit, and this application is installed as a PWA on an iPhone. A blank
// icon everywhere on the one device it is being presented on is not a
// trade worth making, so the sprite is inlined and every reference is
// same-document, which works in every engine and costs one fewer request.
//
// The standalone file is still served at the path above, for anything that
// wants it.
//
// Symbol ids are prefixed: the sprite contains a symbol called "user", and
// ui.html's setup form already has <input id="user">. Two elements sharing an
// id makes getElementById return whichever comes first in the document, which
// would have quietly broken the network-credentials field.
const spritePrefix = "i-"

// injectAssets puts the icon sprite and the loading animation into a page.
//
// Both are markup that every page needs and that must be present before any
// script runs -- the sprite because <use> resolves against the DOM, and the
// loader because one of the waits it covers is the wait for that script to
// arrive. Injecting here keeps a single copy of each: the loader markup lives
// in loader.html and nowhere else, rather than being written once into the
// page for the first paint and again into nav.js for later waits.
//
// The loader goes in as an inert <template>, which nav.js clones when a wait
// passes its threshold. A page that also wants it painted immediately puts a
// <!--LOADER--> comment where it should appear.
//
// That marker paints a LIVE copy, and nothing here or in nav.js ever takes it
// away again -- the copy busy() manages is its own clone of the template. So
// the marker only belongs inside something the page itself removes, the way
// ui.html puts it in #boot and deletes that in hideBoot(). The home page
// carried a bare one at the end of its body for a release: no boot screen, no
// removal, so an orbit animated at the foot of the page for ever, on every
// device, and read as the page never finishing loading.
// TestEveryPagePaintsTheLoaderOnlyWhereItCanBeRemoved is the guard.
func injectAssets(page []byte) []byte {
	page = inlineSprite(page)
	page = injectSkipLink(page)
	page = bytes.Replace(page, []byte("<!--LOADER-->"), loaderHTML, 1)
	add := append(append([]byte(`<template id="ldTpl">`), loaderHTML...),
		[]byte(`</template>`)...)
	add = append(add, pushScriptTag(page)...)
	return injectAfterBody(page, add)
}

// pushScriptTag adds the device-notification client to a page.
//
// On every page, centrally, for the same reason as the chat tab -- and one
// more: signing out stops this device receiving the account's notifications,
// and sign-out is offered on every page. A page without this file would be the
// one where signing out left a handed-over phone still showing its previous
// owner's cases. Deferred: it draws nothing and must not delay the first paint.
func pushScriptTag(page []byte) []byte {
	if bytes.Contains(page, []byte(`src="/push.js"`)) {
		return nil
	}
	return []byte(`<script src="/push.js" defer></script>`)
}

// injectSkipLink puts "skip to content" first in the tab order.
//
// Centrally, because it was written by hand on three pages out of nine and
// forgotten on the other six -- including the report builder, which has the
// longest navigation to tab through of any of them. A link that exists on a
// third of the pages is worse than a convention: somebody who learns to press
// Tab first finds it works, and then one day silently does not.
//
// The target is read from the page's own <main> rather than assumed, because
// the pages disagree: the home page's is id="home" and the others' is
// id="main". A page whose <main> has no id gets one. A page with no <main> at
// all -- the sign-in form, which is a single centred card with no navigation
// to skip -- is left alone rather than given a link pointing nowhere.
func injectSkipLink(page []byte) []byte {
	// Already written by hand on this page: leave it, and leave it first.
	if bytes.Contains(page, []byte(`class="skip"`)) {
		return page
	}
	i := bytes.Index(page, []byte("<main"))
	if i < 0 {
		return page
	}
	rest := page[i+len("<main"):]
	if len(rest) == 0 {
		return page
	}
	if c := rest[0]; c != '>' && c != ' ' && c != '\t' && c != '\r' && c != '\n' {
		return page // a <mainish> element of somebody's own
	}
	end := bytes.IndexByte(rest, '>')
	if end < 0 {
		return page
	}
	tag := rest[:end]

	target := ""
	if j := bytes.Index(tag, []byte(`id="`)); j >= 0 {
		v := tag[j+len(`id="`):]
		if k := bytes.IndexByte(v, '"'); k > 0 {
			target = string(v[:k])
		}
	}
	if target == "" {
		target = "main"
		// Give it the id the link needs, immediately after the element name.
		at := i + len("<main")
		out := make([]byte, 0, len(page)+len(` id="main"`))
		out = append(out, page[:at]...)
		out = append(out, []byte(` id="main"`)...)
		out = append(out, page[at:]...)
		page = out
	}

	return injectAfterBody(page, []byte(
		`<a class="skip" href="#`+target+`">پرش به محتوا</a>`))
}

// inlineSprite returns the page with the sprite inserted just inside <body>.
//
// Hidden with an inline style, not the hidden attribute. The attribute does
// not hide an <svg> here: the sprite computed to display:inline and rendered
// at its intrinsic 150px, pushing every page's header down by 160px. An
// inline style also does not depend on the stylesheet having arrived, which
// was the point of avoiding a CSS class.
//
// display:none does not stop a <use> resolving: a <use> reads the DOM, and a
// <symbol> is never painted where it is defined in any case.
func inlineSprite(page []byte) []byte {
	return injectAfterBody(page, prefixedSprite)
}

// injectAfterBody inserts a block just inside the <body> tag.
//
// The opening tag is found by its name and then scanned to its own '>', rather
// than matched as the literal string "<body>". That is not defensiveness for
// its own sake: this looked for "<body>" exactly, and the first page written
// with an attribute on it -- <body class="home-page"> -- silently got no
// sprite and no loader. Every icon on that page rendered as a blank, which is
// the failure mode a <use> pointing at nothing produces: no error, no console
// warning, just gaps.
func injectAfterBody(page, block []byte) []byte {
	if len(block) == 0 {
		return page
	}
	i := bodyTagIndex(page)
	if i < 0 {
		// No <body> to anchor to: return the page untouched rather than
		// guessing where to put it. A page missing its icons is recoverable; a
		// page with markup injected in the wrong place is not.
		return page
	}
	end := bytes.IndexByte(page[i+len("<body"):], '>')
	if end < 0 {
		// An unterminated opening tag. Nothing sensible to do with it.
		return page
	}
	at := i + len("<body") + end + 1

	out := make([]byte, 0, len(page)+len(block)+1)
	out = append(out, page[:at]...)
	out = append(out, '\n')
	out = append(out, block...)
	out = append(out, page[at:]...)
	return out
}

// bodyTagIndex finds the real <body> element, skipping text that only looks
// like one.
//
// A PLAIN SEARCH FOR "<body" IS NOT ENOUGH, and this is not hypothetical --
// it is the bug this function was written to fix. home.html carries, inside a
// CSS comment in its <style> block, the sentence "an element appended to
// <body> renders underneath a modal's backdrop". That is the first "<body" in
// the document by several thousand bytes. The sprite and the loader template
// were therefore injected INTO A STYLESHEET IN THE HEAD, where a browser
// reads them as CSS text rather than as markup: no symbols in the document,
// every <use href="#i-..."> resolving to nothing, and every icon on the CEO's
// landing page silently blank. It also broke the stylesheet from that comment
// onward, since the injected markup ended the comment early.
//
// There is no error and no console warning for any of that. An icon pointing
// at a symbol that does not exist simply occupies no space.
//
// So the scan skips the three places a "<body" can hide without being one: a
// comment, a <style> and a <script>. Those are exactly the regions an HTML
// parser reads as text rather than as elements, which is the same rule the
// browser applies -- so this agrees with the browser by construction rather
// than by a list of special cases.
func bodyTagIndex(page []byte) int {
	for i := 0; i < len(page); {
		switch {
		case bytes.HasPrefix(page[i:], []byte("<!--")):
			end := bytes.Index(page[i+4:], []byte("-->"))
			if end < 0 {
				return -1 // an unterminated comment swallows the rest
			}
			i += 4 + end + 3
		case isTagStart(page[i:], "style"):
			i = skipRawText(page, i, "style")
		case isTagStart(page[i:], "script"):
			i = skipRawText(page, i, "script")
		case isTagStart(page[i:], "body"):
			return i
		default:
			i++
		}
	}
	return -1
}

// isTagStart reports whether s opens the named element.
//
// The character after the name must end the tag or separate an attribute, so
// this matches <body> and <body class="home-page"> and does not match a
// <bodyish> element of somebody's own.
func isTagStart(s []byte, name string) bool {
	if len(s) < len(name)+1 || s[0] != '<' {
		return false
	}
	if !bytes.EqualFold(s[1:1+len(name)], []byte(name)) {
		return false
	}
	c := s[1+len(name)]
	return c == '>' || c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '/'
}

// skipRawText moves past a <style> or <script> element and everything in it.
//
// Those two hold raw text: a browser does not look for tags inside them, it
// looks only for the matching close tag. Returning the end of the document
// when the close tag is missing is the same thing the browser does -- an
// unclosed <script> really does swallow the rest of the page.
func skipRawText(page []byte, i int, name string) int {
	closing := []byte("</" + name)
	rest := page[i:]
	end := indexFold(rest, closing)
	if end < 0 {
		return len(page)
	}
	return i + end + len(closing)
}

// indexFold is bytes.Index, case-insensitively.
func indexFold(s, sub []byte) int {
	if len(sub) == 0 || len(s) < len(sub) {
		return -1
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if bytes.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// prefixedSprite is the built sprite with its symbol ids namespaced and the
// root marked hidden.
var prefixedSprite = buildPrefixedSprite()

func buildPrefixedSprite() []byte {
	raw, err := staticFS.ReadFile("static/vendor/lucide/sprite.svg")
	if err != nil {
		return nil
	}
	raw = bytes.ReplaceAll(raw, []byte(`<symbol id="`), []byte(`<symbol id="`+spritePrefix))
	raw = bytes.Replace(raw, []byte(`<svg `), []byte(`<svg style="display:none" `), 1)
	return bytes.TrimRight(raw, "\n")
}

// The reference form is
//
//	<svg class="ic" aria-hidden="true"><use href="#i-search"/></svg>
//
// and every page writes it directly. There used to be an iconMarkup() helper
// here to build that string, kept "because it documents the intended form" --
// but nothing had called it for a long time, and a function no caller
// exercises documents whatever it has drifted into rather than what the pages
// actually do. The comment above is the documentation; the pages are the
// implementation.
