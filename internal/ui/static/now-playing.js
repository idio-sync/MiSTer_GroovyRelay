// Protect the global now-playing range controls from htmx poll swaps while the
// operator is actively dragging or keyboard-adjusting them.
(function () {
	var activeRange = null;

	function playbackRangeFromEvent(ev) {
		var target = ev.target;
		if (!target || !target.closest) { return null; }
		return target.closest('#gr-now-playing .gr-now-playing-seek input[type="range"], #gr-now-playing .gr-output-volume input[type="range"]');
	}

	function bannerFor(input) {
		return input && input.closest ? input.closest('#gr-now-playing') : null;
	}

	function markInteracting(input) {
		var banner = bannerFor(input);
		if (!banner) { return; }
		activeRange = input;
		banner.setAttribute('data-seek-interacting', 'true');
	}

	function clearInteracting(input) {
		var banner = bannerFor(input);
		if (banner) {
			banner.removeAttribute('data-seek-interacting');
		}
		if (activeRange === input) {
			activeRange = null;
		}
	}

	function clearActiveSoon() {
		var input = activeRange;
		if (!input) { return; }
		window.setTimeout(function () {
			clearInteracting(input);
		}, 0);
	}

	document.addEventListener('pointerdown', function (ev) {
		var input = playbackRangeFromEvent(ev);
		if (input) { markInteracting(input); }
	}, true);

	document.addEventListener('focusin', function (ev) {
		var input = playbackRangeFromEvent(ev);
		if (input) { markInteracting(input); }
	}, true);

	document.addEventListener('pointerup', clearActiveSoon, true);
	document.addEventListener('pointercancel', clearActiveSoon, true);

	document.addEventListener('change', function (ev) {
		if (playbackRangeFromEvent(ev)) { clearActiveSoon(); }
	}, true);

	document.addEventListener('blur', function (ev) {
		if (playbackRangeFromEvent(ev)) { clearActiveSoon(); }
	}, true);

	document.addEventListener('htmx:beforeSwap', function (ev) {
		var detail = ev.detail || {};
		var target = detail.target;
		var cfg = detail.requestConfig || {};
		var path = (cfg.path || '').split('?')[0];
		var verb = (cfg.verb || '').toLowerCase();
		if (!target || target.id !== 'gr-now-playing') { return; }
		if (!target.hasAttribute('data-seek-interacting')) { return; }
		if (verb === 'get' && path === '/ui/playback/banner') {
			ev.preventDefault();
		}
	});
})();
