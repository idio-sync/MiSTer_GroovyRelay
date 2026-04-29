// Copy-to-clipboard with secure-context fallback (Spec §6.2).
//
// Buttons declare data-copy-target="<css-selector>" pointing at the
// element whose textContent should be copied. One delegated listener
// on <body> handles clicks for any current or future button, so htmx
// OOB swaps that inject new toasts work without rebinding.
//
// Two execution paths:
//   1. navigator.clipboard.writeText — fast, async; only works in
//      secure contexts (HTTPS or http://localhost). Most LAN
//      deployments hit http://192.168.x.x and skip this entirely.
//   2. document.execCommand('copy') via a hidden <textarea> — legacy
//      but still supported in current Chromium and Firefox; works on
//      plain HTTP. Falls through here when (1) is unavailable or
//      rejects.
//
// Button text is replaced with "copied" or "copy failed" for 1.5s,
// then restored. Failures are silent beyond that — by design, the
// docker command is always still visible in the <pre> for manual
// triple-click + Ctrl-C.
(function () {
	function execFallback(text) {
		var ta = document.createElement('textarea');
		ta.value = text;
		ta.setAttribute('readonly', '');
		ta.style.position = 'fixed';
		ta.style.top = '-1000px';
		ta.style.opacity = '0';
		document.body.appendChild(ta);
		ta.select();
		var ok = false;
		try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
		document.body.removeChild(ta);
		return ok;
	}
	function copyText(text) {
		if (window.isSecureContext && navigator.clipboard) {
			return navigator.clipboard.writeText(text).then(
				function () { return true; },
				function () { return execFallback(text); }
			);
		}
		return Promise.resolve(execFallback(text));
	}
	document.addEventListener('click', function (ev) {
		var btn = ev.target.closest && ev.target.closest('[data-copy-target]');
		if (!btn) { return; }
		var sel = btn.getAttribute('data-copy-target');
		var target = sel ? document.querySelector(sel) : null;
		if (!target) { return; }
		var original = btn.textContent;
		Promise.resolve(copyText(target.textContent)).then(function (ok) {
			btn.textContent = ok ? 'copied' : 'copy failed';
			setTimeout(function () { btn.textContent = original; }, 1500);
		});
	});
})();
