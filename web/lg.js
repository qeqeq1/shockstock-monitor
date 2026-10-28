"use strict";

const lgElements = {
	search: document.querySelector("#search"),
	stockFilter: document.querySelector("#stockFilter"),
	sizeSelector: document.querySelector("#sizeSelector"),
	locationGrid: document.querySelector("#locationGrid"),
	resultCount: document.querySelector("#resultCount"),
	toast: document.querySelector("#toast"),
};

let lgState = null;
let selectedSize = "1GB";
let toastTimer = null;

document.addEventListener("DOMContentLoaded", () => {
	lgElements.search.addEventListener("input", renderLocations);
	lgElements.stockFilter.addEventListener("change", renderLocations);
	lgElements.sizeSelector.addEventListener("click", handleSizeChange);
	lgElements.locationGrid.addEventListener("click", handleGridClick);
	loadLGState();
	window.setInterval(() => loadLGState(true), 30000);
});

async function loadLGState(silent = false) {
	try {
		const response = await fetch("/api/public/stock", {
			headers: { Accept: "application/json" },
		});
		if (!response.ok) {
			throw new Error(`库存接口返回 HTTP ${response.status}`);
		}
		lgState = await response.json();
		renderSummary();
		renderLocations();
	} catch (error) {
		setUpdateStatus("error", "库存读取失败");
		if (!silent) {
			lgElements.locationGrid.innerHTML = `
				<div class="empty-state">
					<strong>无法读取库存</strong>
					<span>${escapeHTML(error.message)}</span>
				</div>
			`;
		}
	}
}

function renderSummary() {
	if (!lgState) {
		return;
	}
	const summary = lgState.summary;
	setText("#summaryLocations", String(lgState.locations.length));
	setText("#summaryAvailable", String(summary.available));
	setText("#summaryAvailableDetail", `共 ${summary.total} 个套餐`);
	setText("#summaryWatched", String(summary.watched));
	setText("#summarySource", lgState.lastCheck?.ok ? "在线" : "异常");
	setText("#summarySourceDetail", lgState.lastCheck?.ok ? "库存接口正常" : "请查看管理后台");
	const lastCheck = lgState.lastCheck?.finishedAt;
	setText("#lastCheck", lastCheck ? formatDateTime(lastCheck) : "尚未检测");
	setUpdateStatus(lgState.lastCheck?.ok ? "ok" : "error", lgState.lastCheck?.ok ? "库存源在线" : "库存源异常");
}

function renderLocations() {
	if (!lgState) {
		return;
	}
	const query = lgElements.search.value.trim().toLowerCase();
	const stockFilter = lgElements.stockFilter.value;
	const visible = lgState.locations.filter((location) => {
		if (stockFilter === "available" && location.available === 0) {
			return false;
		}
		if (stockFilter === "out" && location.available > 0) {
			return false;
		}
		if (!query) {
			return true;
		}
		const haystack = [
			location.code,
			location.name,
			location.facility,
			location.ipv4,
			location.ipv6,
			...location.products.map((product) => `${product.name} ${product.storage} ${product.cpu}`),
		].join(" ").toLowerCase();
		return haystack.includes(query);
	});

	if (visible.length === 0) {
		lgElements.locationGrid.innerHTML = `
			<div class="empty-state">
				<strong>没有匹配的机房</strong>
				<span>调整搜索或库存筛选条件。</span>
			</div>
		`;
	} else {
		lgElements.locationGrid.innerHTML = visible.map(locationCard).join("");
	}
	lgElements.resultCount.textContent = `${visible.length} / ${lgState.locations.length} 个机房`;
}

function locationCard(location) {
	const products = location.products.map(productRow).join("");
	return `
		<article class="location-card">
			<header class="location-head">
				<div>
					<h2>${escapeHTML(location.name)}</h2>
					<p>${escapeHTML(location.facility)} · ${escapeHTML(location.code.toUpperCase())}</p>
				</div>
				<span class="stock-count ${location.available > 0 ? "has-stock" : ""}">
					${location.available} / ${location.total} 有货
				</span>
			</header>
			<div class="network-block">
				<div class="network-row">
					<span class="network-label">源站 IPv4</span>
					<code class="network-value">${escapeHTML(location.ipv4)}</code>
					<button class="icon-button" type="button" data-copy="${escapeAttribute(location.ipv4)}" data-copy-label="IPv4" title="复制 IPv4" aria-label="复制 IPv4">
						${copyIcon()}
					</button>
				</div>
				<div class="network-row">
					<span class="network-label">源站 IPv6</span>
					<code class="network-value">${escapeHTML(location.ipv6)}</code>
					<button class="icon-button" type="button" data-copy="${escapeAttribute(location.ipv6)}" data-copy-label="IPv6" title="复制 IPv6" aria-label="复制 IPv6">
						${copyIcon()}
					</button>
				</div>
				<div class="network-actions">
					<a class="button primary" href="${escapeAttribute(location.domain + "/" + selectedSize + ".test")}" target="_blank" rel="noopener">
						${downloadIcon()} 下载 ${selectedSize}
					</a>
					<button class="button secondary" type="button" data-copy="${escapeAttribute(directDownloadCommand(location))}" data-copy-label="直连测速命令">
						${terminalIcon()} 直连测速命令
					</button>
					<button class="button secondary" type="button" data-copy="${escapeAttribute(ipCommand("ping", location.ipv4))}" data-copy-label="Ping 命令">
						${terminalIcon()} Ping 命令
					</button>
					<button class="button secondary" type="button" data-copy="${escapeAttribute(ipCommand("trace", location.ipv4))}" data-copy-label="Traceroute 命令">
						${terminalIcon()} Traceroute
					</button>
					<a class="button secondary" href="${escapeAttribute(location.domain)}" target="_blank" rel="noopener">
						${networkIcon()} 官方 LG
					</a>
				</div>
			</div>
			<div class="products">
				${products}
			</div>
		</article>
	`;
}

function productRow(product) {
	let badgeClass = "unknown";
	let badgeText = "未检测";
	if (product.checked) {
		if (product.stock > 0) {
			badgeClass = "in";
			badgeText = `${product.stock} 台`;
		} else {
			badgeClass = "out";
			badgeText = "售罄";
		}
	}
	const disabled = !product.checked || product.stock <= 0;
	return `
		<div class="product-row">
			<div>
				<div class="product-name">${escapeHTML(product.name)}</div>
				<div class="product-spec">${escapeHTML(product.storage)} · ${product.ramGb} GB</div>
			</div>
			<div class="product-spec">${escapeHTML(product.cpu)}</div>
			<div class="product-price">$${Number(product.price).toFixed(2)}/月</div>
			<div class="product-action">
				<span class="stock-badge ${badgeClass}">${escapeHTML(badgeText)}</span>
				<a class="buy-link ${disabled ? "disabled" : ""}" href="${escapeAttribute(product.buyUrl)}" target="_blank" rel="noopener nofollow" ${disabled ? 'tabindex="-1" aria-disabled="true"' : ""}>${disabled ? "售罄" : "购买"}</a>
			</div>
		</div>
	`;
}

function handleSizeChange(event) {
	const button = event.target.closest("button[data-size]");
	if (!button) {
		return;
	}
	selectedSize = button.dataset.size;
	lgElements.sizeSelector.querySelectorAll("button").forEach((item) => {
		item.classList.toggle("active", item.dataset.size === selectedSize);
	});
	renderLocations();
}

async function handleGridClick(event) {
	const copyButton = event.target.closest("[data-copy]");
	if (!copyButton) {
		return;
	}
	try {
		await copyText(copyButton.dataset.copy);
		showToast(`${copyButton.dataset.copyLabel || "内容"}已复制`);
	} catch {
		showToast("复制失败，请手动选择文本");
	}
}

function ipCommand(type, ip) {
	const windows = /Windows/i.test(navigator.userAgent);
	if (type === "ping") {
		return windows ? `ping -n 4 ${ip}` : `ping -c 4 ${ip}`;
	}
	return windows ? `tracert ${ip}` : `traceroute ${ip}`;
}

function directDownloadCommand(location) {
	const windows = /Windows/i.test(navigator.userAgent);
	const curl = windows ? "curl.exe" : "curl";
	const output = windows ? "NUL" : "/dev/null";
	const host = new URL(location.domain).hostname;
	return `${curl} --resolve ${host}:443:${location.ipv4} -o ${output} ${location.domain}/${selectedSize}.test`;
}

function setUpdateStatus(cls, text) {
	const element = document.querySelector("#updateStatus");
	element.classList.remove("ok", "error");
	element.classList.add(cls);
	element.title = text;
}

function setText(selector, value) {
	const element = document.querySelector(selector);
	if (element) {
		element.textContent = value;
	}
}

function formatDateTime(value) {
	const date = new Date(value);
	if (!value || Number.isNaN(date.getTime())) {
		return "—";
	}
	return new Intl.DateTimeFormat("zh-CN", {
		month: "2-digit",
		day: "2-digit",
		hour: "2-digit",
		minute: "2-digit",
		second: "2-digit",
		hour12: false,
	}).format(date);
}

async function copyText(value) {
	if (navigator.clipboard?.writeText) {
		await navigator.clipboard.writeText(value);
		return;
	}
	const textarea = document.createElement("textarea");
	textarea.value = value;
	textarea.style.position = "fixed";
	textarea.style.opacity = "0";
	document.body.appendChild(textarea);
	textarea.select();
	document.execCommand("copy");
	textarea.remove();
}

function showToast(message) {
	window.clearTimeout(toastTimer);
	lgElements.toast.textContent = message;
	lgElements.toast.hidden = false;
	toastTimer = window.setTimeout(() => {
		lgElements.toast.hidden = true;
	}, 2600);
}

function escapeHTML(value) {
	return String(value ?? "")
		.replaceAll("&", "&amp;")
		.replaceAll("<", "&lt;")
		.replaceAll(">", "&gt;")
		.replaceAll('"', "&quot;")
		.replaceAll("'", "&#039;");
}

function escapeAttribute(value) {
	return escapeHTML(value);
}

function copyIcon() {
	return '<svg aria-hidden="true" viewBox="0 0 24 24"><rect width="14" height="14" x="8" y="8" rx="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>';
}

function terminalIcon() {
	return '<svg aria-hidden="true" viewBox="0 0 24 24"><polyline points="4 17 10 11 4 5"/><line x1="12" x2="20" y1="19" y2="19"/></svg>';
}

function networkIcon() {
	return '<svg aria-hidden="true" viewBox="0 0 24 24"><rect width="6" height="6" x="9" y="2" rx="1"/><rect width="6" height="6" x="2" y="16" rx="1"/><rect width="6" height="6" x="16" y="16" rx="1"/><path d="M12 8v4"/><path d="M5 16v-2h14v2"/></svg>';
}

function downloadIcon() {
	return '<svg aria-hidden="true" viewBox="0 0 24 24"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" x2="12" y1="15" y2="3"/></svg>';
}
