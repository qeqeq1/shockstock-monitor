"use strict";

const elements = {
	loginView: document.querySelector("#loginView"),
	appView: document.querySelector("#appView"),
	loginForm: document.querySelector("#loginForm"),
	loginPassword: document.querySelector("#loginPassword"),
	loginError: document.querySelector("#loginError"),
	settingsForm: document.querySelector("#settingsForm"),
	passwordForm: document.querySelector("#passwordForm"),
	productRows: document.querySelector("#productRows"),
	productEmpty: document.querySelector("#productEmpty"),
	productSearch: document.querySelector("#productSearch"),
	locationFilter: document.querySelector("#locationFilter"),
	stockFilter: document.querySelector("#stockFilter"),
	watchVisibleButton: document.querySelector("#watchVisibleButton"),
	unwatchVisibleButton: document.querySelector("#unwatchVisibleButton"),
	checkNowButton: document.querySelector("#checkNowButton"),
	testEmailButton: document.querySelector("#testEmailButton"),
	logoutButton: document.querySelector("#logoutButton"),
	serviceState: document.querySelector("#serviceState"),
	serviceStateText: document.querySelector("#serviceStateText"),
	toast: document.querySelector("#toast"),
	apiToken: document.querySelector("#apiToken"),
	curlExample: document.querySelector("#curlExample"),
};

let state = null;
let activeTab = "overview";
let toastTimer = null;

document.addEventListener("DOMContentLoaded", initialize);

async function initialize() {
	bindEvents();

	try {
		await loadState();
	} catch (error) {
		if (error.status !== 401) {
			showToast(error.message, "error");
		}
		showLogin();
	}

	window.setInterval(updateCountdown, 1000);
	window.setInterval(() => {
		if (!elements.appView.hidden && activeTab === "overview") {
			loadState(false).catch(() => {});
		}
	}, 15000);
}

function bindEvents() {
	elements.loginForm.addEventListener("submit", handleLogin);
	elements.settingsForm.addEventListener("submit", handleSettingsSave);
	elements.passwordForm.addEventListener("submit", handlePasswordChange);
	elements.checkNowButton.addEventListener("click", handleCheckNow);
	elements.testEmailButton.addEventListener("click", handleTestEmail);
	elements.logoutButton.addEventListener("click", handleLogout);
	elements.productRows.addEventListener("change", handleWatchChange);
	elements.watchVisibleButton.addEventListener("click", () => setVisibleWatched(true));
	elements.unwatchVisibleButton.addEventListener("click", () => setVisibleWatched(false));
	elements.productSearch.addEventListener("input", renderProducts);
	elements.locationFilter.addEventListener("change", renderProducts);
	elements.stockFilter.addEventListener("change", renderProducts);

	document.querySelectorAll(".tab").forEach((tab) => {
		tab.addEventListener("click", () => switchTab(tab.dataset.tab));
	});
	document.querySelectorAll("[data-goto-tab]").forEach((button) => {
		button.addEventListener("click", () => switchTab(button.dataset.gotoTab));
	});
	document.querySelectorAll(".password-toggle").forEach((button) => {
		button.addEventListener("click", () => togglePassword(button));
	});

	document.querySelector("#revealTokenButton").addEventListener("click", toggleToken);
	document.querySelector("#copyTokenButton").addEventListener("click", copyToken);
	document.querySelector("#copyCurlButton").addEventListener("click", copyCurl);
	document.querySelector("#rotateTokenButton").addEventListener("click", rotateToken);
}

async function handleLogin(event) {
	event.preventDefault();
	elements.loginError.hidden = true;
	const submit = elements.loginForm.querySelector("button[type=submit]");
	setBusy(submit, true);
	try {
		await api("/admin/api/login", {
			method: "POST",
			body: JSON.stringify({ password: elements.loginPassword.value }),
		});
		elements.loginPassword.value = "";
		await loadState();
	} catch (error) {
		elements.loginError.textContent = error.message;
		elements.loginError.hidden = false;
		elements.loginPassword.select();
	} finally {
		setBusy(submit, false);
	}
}

async function handleLogout() {
	try {
		await api("/admin/api/logout", {
			method: "POST",
			body: JSON.stringify({}),
		});
	} catch (error) {
		if (error.status !== 401) {
			showToast(error.message, "error");
		}
	}
	state = null;
	showLogin();
}

async function handleCheckNow() {
	setBusy(elements.checkNowButton, true);
	try {
		await api("/admin/api/check", {
			method: "POST",
			body: JSON.stringify({}),
		});
		showToast("检测任务已加入队列", "success");
		[1800, 5000, 12000, 24000].forEach((delay) => {
			window.setTimeout(() => loadState(false).catch(() => {}), delay);
		});
	} catch (error) {
		showToast(error.message, "error");
	} finally {
		window.setTimeout(() => setBusy(elements.checkNowButton, false), 700);
	}
}

async function handleSettingsSave(event) {
	event.preventDefault();
	await saveSettings(false);
}

async function saveSettings(silent) {
	const button = elements.settingsForm.querySelector("button[type=submit]");
	setBusy(button, true);
	try {
		const response = await api("/admin/api/settings", {
			method: "PUT",
			body: JSON.stringify(settingsPayload()),
		});
		if (state) {
			state.settings = response;
		}
		elements.smtpPassword.value = "";
		if (!silent) {
			showToast("设置已保存，下一次检测将使用新配置", "success");
		}
		await loadState(false);
		return true;
	} catch (error) {
		showToast(error.message, "error");
		return false;
	} finally {
		setBusy(button, false);
	}
}

async function handleTestEmail() {
	setBusy(elements.testEmailButton, true);
	try {
		const saved = await saveSettings(true);
		if (!saved) {
			return;
		}
		await api("/admin/api/test-email", {
			method: "POST",
			body: JSON.stringify({}),
		});
		showToast("测试邮件已发送，请检查收件箱", "success");
		await loadState(false);
	} catch (error) {
		showToast(error.message, "error");
	} finally {
		setBusy(elements.testEmailButton, false);
	}
}

async function handlePasswordChange(event) {
	event.preventDefault();
	const currentPassword = document.querySelector("#currentPassword").value;
	const newPassword = document.querySelector("#newPassword").value;
	const confirmPassword = document.querySelector("#confirmPassword").value;
	if (newPassword !== confirmPassword) {
		showToast("两次输入的新密码不一致", "error");
		return;
	}
	const button = elements.passwordForm.querySelector("button[type=submit]");
	setBusy(button, true);
	try {
		await api("/admin/api/password", {
			method: "PUT",
			body: JSON.stringify({ currentPassword, newPassword }),
		});
		elements.passwordForm.reset();
		showToast("管理密码已更新", "success");
		await loadState(false);
	} catch (error) {
		showToast(error.message, "error");
	} finally {
		setBusy(button, false);
	}
}

async function handleWatchChange(event) {
	const input = event.target.closest("input[data-watch-id]");
	if (!input) {
		return;
	}
	input.disabled = true;
	try {
		await updateWatch([input.dataset.watchId], input.checked);
		await loadState(false);
	} catch (error) {
		input.checked = !input.checked;
		showToast(error.message, "error");
	} finally {
		input.disabled = false;
	}
}

async function setVisibleWatched(watched) {
	const products = filteredProducts();
	if (products.length === 0) {
		showToast("当前筛选结果为空", "error");
		return;
	}
	const button = watched ? elements.watchVisibleButton : elements.unwatchVisibleButton;
	setBusy(button, true);
	try {
		await updateWatch(products.map((product) => product.id), watched);
		showToast(
			watched ? `已监控 ${products.length} 个套餐` : `已取消监控 ${products.length} 个套餐`,
			"success",
		);
		await loadState(false);
	} catch (error) {
		showToast(error.message, "error");
	} finally {
		setBusy(button, false);
	}
}

async function updateWatch(ids, watched) {
	await api("/admin/api/watch", {
		method: "PUT",
		body: JSON.stringify({ ids, watched }),
	});
}

async function rotateToken() {
	if (!window.confirm("重新生成后，旧 Token 会立即失效。继续吗？")) {
		return;
	}
	const button = document.querySelector("#rotateTokenButton");
	setBusy(button, true);
	try {
		const response = await api("/admin/api/token/rotate", {
			method: "POST",
			body: JSON.stringify({}),
		});
		if (state) {
			state.apiToken = response.token;
			renderAPI();
		}
		showToast("API Token 已重新生成", "success");
	} catch (error) {
		showToast(error.message, "error");
	} finally {
		setBusy(button, false);
	}
}

async function loadState(render = true) {
	const nextState = await api("/admin/api/state");
	state = nextState;
	if (render) {
		renderState();
	} else {
		renderDynamicSections();
	}
	showApp();
}

function renderState() {
	renderSummary();
	renderSettings();
	renderProductFilters();
	renderProducts();
	renderEvents();
	renderNotifications();
	renderLogs();
	renderAPI();
	renderSystem();
	renderDynamicSections();
}

function renderDynamicSections() {
	renderSummary();
	renderLastCheck();
	renderEvents();
	renderNotifications();
	renderLogs();
	renderProducts();
	updateCountdown();
}

function renderSummary() {
	if (!state) {
		return;
	}
	const summary = state.summary;
	setText("#statWatched", `${summary.watched}`);
	setText("#statTotal", `共 ${summary.total} 个`);
	setText("#statAvailable", `${summary.available}`);
	setText("#statAvailableWatched", `监控项 ${summary.watchedLive}`);
	setText("#statOut", `${summary.outOfStock}`);
	setText("#statInterval", `${state.settings.pollIntervalSeconds} 秒`);
	setText("#statCheckedAt", state.lastCheck ? `最近 ${formatRelative(state.lastCheck.finishedAt)}` : "尚未检测");
}

function renderLastCheck() {
	const check = state?.lastCheck;
	const band = document.querySelector(".status-band");
	band.classList.remove("ok", "error");
	elements.serviceState.classList.remove("ok", "error");

	if (!check) {
		setText("#serviceStateText", "等待首次检测");
		setText("#overviewStatusTitle", "等待首次检测");
		setText("#overviewStatusDetail", "服务启动后会立即检查一次库存。");
		setText("#lastCheckHeading", "尚未检测");
		return;
	}

	setText("#lastCheckHeading", formatDateTime(check.finishedAt));
	if (check.ok) {
		band.classList.add("ok");
		elements.serviceState.classList.add("ok");
		setText("#serviceStateText", "库存源在线");
		setText("#overviewStatusTitle", "库存检测正常");
		setText(
			"#overviewStatusDetail",
			`${check.totalProducts} 个套餐，${check.available} 个有货${check.newStock ? `，新增补货 ${check.newStock} 个` : ""}`,
		);
	} else {
		band.classList.add("error");
		elements.serviceState.classList.add("error");
		setText("#serviceStateText", "检测异常");
		setText("#overviewStatusTitle", "最近一次检测失败");
		setText("#overviewStatusDetail", check.error || "无法访问库存源，服务会自动重试。");
	}
}

function renderSettings() {
	if (!state?.settings) {
		return;
	}
	const settings = state.settings;
	const smtp = settings.smtp || {};
	setFieldValue("#pollInterval", settings.pollIntervalSeconds);
	setFieldValue("#smtpEnabled", Boolean(smtp.enabled), true);
	setFieldValue("#smtpHost", smtp.host || "");
	setFieldValue("#smtpPort", smtp.port || 587);
	setFieldValue("#smtpSecurity", smtp.security || "starttls");
	setFieldValue("#smtpUsername", smtp.username || "");
	setFieldValue("#smtpPassword", "");
	setFieldValue("#smtpFrom", smtp.from || "");
	setFieldValue("#smtpSubjectPrefix", smtp.subjectPrefix || "[Shock Stock]");
	setFieldValue("#smtpRecipients", (smtp.recipients || []).join("\n"));
	document.querySelector("#smtpPassword").placeholder = smtp.hasPassword
		? "已保存密码，留空保持不变"
		: "请输入 SMTP 密码";
}

function settingsPayload() {
	return {
		pollIntervalSeconds: Number(document.querySelector("#pollInterval").value),
		smtp: {
			enabled: document.querySelector("#smtpEnabled").checked,
			host: document.querySelector("#smtpHost").value.trim(),
			port: Number(document.querySelector("#smtpPort").value),
			security: document.querySelector("#smtpSecurity").value,
			username: document.querySelector("#smtpUsername").value.trim(),
			password: document.querySelector("#smtpPassword").value,
			from: document.querySelector("#smtpFrom").value.trim(),
			recipients: document.querySelector("#smtpRecipients").value
				.split(/[\n,;]+/)
				.map((value) => value.trim())
				.filter(Boolean),
			subjectPrefix: document.querySelector("#smtpSubjectPrefix").value.trim(),
		},
	};
}

function renderProductFilters() {
	const current = elements.locationFilter.value || "all";
	const locations = new Map();
	state.products.forEach((product) => {
		locations.set(product.locationCode, product.locationName);
	});
	const options = [...locations.entries()]
		.sort((a, b) => a[1].localeCompare(b[1]))
		.map(([code, name]) => `<option value="${escapeHTML(code)}">${escapeHTML(name)}</option>`)
		.join("");
	elements.locationFilter.innerHTML = `<option value="all">所有机房</option>${options}`;
	elements.locationFilter.value = locations.has(current) ? current : "all";
}

function filteredProducts() {
	if (!state) {
		return [];
	}
	const search = elements.productSearch.value.trim().toLowerCase();
	const location = elements.locationFilter.value;
	const stock = elements.stockFilter.value;
	return state.products.filter((product) => {
		if (location !== "all" && product.locationCode !== location) {
			return false;
		}
		if (stock === "available" && product.stock <= 0) {
			return false;
		}
		if (stock === "out" && (!product.checked || product.stock > 0)) {
			return false;
		}
		if (stock === "unknown" && product.checked) {
			return false;
		}
		if (stock === "watched" && !product.watched) {
			return false;
		}
		if (search) {
			const haystack = [
				product.name,
				product.locationName,
				product.cpu,
				`${product.ramGb} GB`,
				product.storage,
				product.id,
			].join(" ").toLowerCase();
			if (!haystack.includes(search)) {
				return false;
			}
		}
		return true;
	});
}

function renderProducts() {
	if (!state) {
		return;
	}
	const products = filteredProducts();
	elements.productRows.innerHTML = products.map(productRow).join("");
	elements.productEmpty.hidden = products.length > 0;
	document.querySelector(".product-table").hidden = products.length === 0;
	setText("#productResultCount", `${products.length} 个套餐`);
	setText("#watchedResultCount", `${products.filter((product) => product.watched).length} 个已监控`);
}

function productRow(product) {
	let stockClass = "unknown";
	let stockText = "未检查";
	if (product.checked) {
		if (product.stock > 0) {
			stockClass = "available";
			stockText = `${product.stock} 台可用`;
		} else {
			stockClass = "out";
			stockText = "售罄";
		}
	}
	let pending = "";
	if (product.alertPending) {
		pending = `<span class="pending-badge">待提醒 ${product.alertCount + 1}/3</span>`;
	} else if (product.alertCount > 0 && product.nextAlertAt) {
		pending = `<span class="pending-badge">已提醒 ${product.alertCount}/3</span>`;
	}
	return `
		<tr>
			<td>
				<div class="product-name">
					<strong>${escapeHTML(product.name)}</strong>
					<a href="${escapeAttribute(product.orderUrl)}" target="_blank" rel="noreferrer">商品 #${escapeHTML(product.id)}</a>
				</div>
			</td>
			<td>
				<div class="location-cell">
					<strong>${escapeHTML(product.locationName)}</strong>
					<span>${escapeHTML(product.locationCode.toUpperCase())}</span>
				</div>
			</td>
			<td>
				<div class="hardware-cell">
					<strong>${escapeHTML(product.storage)}</strong>
					<span>${escapeHTML(product.cpu)} · ${product.ramGb} GB</span>
				</div>
			</td>
			<td class="price-cell">$${product.priceMonthly.toFixed(2)}/月</td>
			<td>
				<span class="stock-badge ${stockClass}">${escapeHTML(stockText)}</span>
				${pending}
			</td>
			<td>
				<label class="watch-toggle" title="${product.watched ? "取消监控" : "开始监控"}">
					<input type="checkbox" data-watch-id="${escapeAttribute(product.id)}" ${product.watched ? "checked" : ""} aria-label="监控 ${escapeAttribute(product.name)}">
					<span aria-hidden="true"></span>
				</label>
			</td>
		</tr>
	`;
}

function renderEvents() {
	const target = document.querySelector("#recentEvents");
	if (!state.stockEvents.length) {
		target.innerHTML = emptyState("暂无库存变化", "首次检测后，变化会记录在这里。");
		return;
	}
	target.innerHTML = state.stockEvents
		.slice(0, 8)
		.map((event) => {
			const delta = event.newStock - event.oldStock;
			const title = delta > 0 ? `${event.product} 补货 ${delta} 台` : `${event.product} 库存更新`;
			return activityItem(title, `${event.oldStock} → ${event.newStock}`, event.createdAt);
		})
		.join("");
}

function renderNotifications() {
	const target = document.querySelector("#recentNotifications");
	if (!state.notifications.length) {
		target.innerHTML = emptyState("暂无邮件记录", "补货提醒和测试邮件会显示在这里。");
		return;
	}
	target.innerHTML = state.notifications
		.slice(0, 8)
		.map((record) => {
			const status = record.status === "sent"
				? "已发送"
				: record.status === "test"
					? "测试邮件"
					: "发送失败";
			const detail = record.error || record.recipients || "无收件人信息";
			return activityItem(record.subject || status, detail, record.createdAt);
		})
		.join("");
}

function renderLogs() {
	const logs = state.logs || [];
	const markup = logs.length
		? logs.map(logItem).join("")
		: emptyState("暂无日志", "服务活动会显示在这里。");
	document.querySelector("#recentLogs").innerHTML = markup;
	document.querySelector("#systemLogs").innerHTML = markup;
}

function logItem(record) {
	const level = ["info", "warn", "error"].includes(record.level) ? record.level : "info";
	return `
		<div class="log-item">
			<span class="log-level ${level}">${escapeHTML(level)}</span>
			<span class="log-message">${escapeHTML(record.message)}</span>
			<span class="log-time">${escapeHTML(formatDateTime(record.createdAt))}</span>
		</div>
	`;
}

function renderAPI() {
	elements.apiToken.value = state.apiToken;
	const origin = window.location.origin;
	elements.curlExample.textContent = [
		`export SHOCK_API_TOKEN='${state.apiToken}'`,
		"",
		`curl -sS -H "Authorization: Bearer $SHOCK_API_TOKEN" \\`,
		`  "${origin}/api/v1/stock?location=jp"`,
		"",
		`curl -sS -X POST -H "Authorization: Bearer $SHOCK_API_TOKEN" \\`,
		`  "${origin}/api/v1/check"`,
	].join("\n");
}

function renderSystem() {
	setText("#systemVersion", state.version);
	setText("#currentOrigin", window.location.origin);
	setText("#systemSource", state.sourceUrl);
	setText("#systemUptime", formatDuration(Date.now() - new Date(state.startedAt).getTime()));
	setText("#sourceLabel", state.sourceUrl);
}

function updateCountdown() {
	if (!state || !state.nextCheckAt) {
		setText("#nextCheckText", "等待调度");
		return;
	}
	const diff = new Date(state.nextCheckAt).getTime() - Date.now();
	if (diff <= 0) {
		setText("#nextCheckText", "即将检测");
		return;
	}
	const seconds = Math.max(1, Math.ceil(diff / 1000));
	if (seconds < 60) {
		setText("#nextCheckText", `${seconds} 秒后`);
	} else {
		setText("#nextCheckText", `${Math.ceil(seconds / 60)} 分钟后`);
	}
}

function switchTab(tabName) {
	activeTab = tabName;
	document.querySelectorAll(".tab").forEach((tab) => {
		const active = tab.dataset.tab === tabName;
		tab.classList.toggle("active", active);
		tab.setAttribute("aria-selected", String(active));
	});
	document.querySelectorAll(".tab-panel").forEach((panel) => {
		const active = panel.id === `tab-${tabName}`;
		panel.classList.toggle("active", active);
		panel.hidden = !active;
	});
	if (tabName === "products") {
		renderProducts();
	}
	if (tabName === "notifications") {
		renderSettings();
		loadState(false).catch((error) => showToast(error.message, "error"));
	}
	if (tabName === "api") {
		loadState(false).catch((error) => showToast(error.message, "error"));
	}
}

async function api(path, options = {}) {
	const headers = {
		Accept: "application/json",
		...(options.headers || {}),
	};
	if (options.body) {
		headers["Content-Type"] = "application/json";
	}
	const response = await fetch(path, {
		credentials: "same-origin",
		...options,
		headers,
	});
	let payload = null;
	if (response.status !== 204) {
		const text = await response.text();
		if (text) {
			try {
				payload = JSON.parse(text);
			} catch {
				payload = null;
			}
		}
	}
	if (!response.ok) {
		const error = new Error(payload?.error || `请求失败（HTTP ${response.status}）`);
		error.status = response.status;
		if (response.status === 401 && path !== "/admin/api/login") {
			state = null;
			showLogin("登录会话已过期，请重新登录");
		}
		throw error;
	}
	return payload;
}

function showApp() {
	elements.loginView.hidden = true;
	elements.appView.hidden = false;
}

function showLogin(message = "") {
	elements.appView.hidden = true;
	elements.loginView.hidden = false;
	if (message) {
		elements.loginError.textContent = message;
		elements.loginError.hidden = false;
	}
	window.setTimeout(() => elements.loginPassword.focus(), 20);
}

function setBusy(button, busy) {
	if (!button) {
		return;
	}
	button.disabled = busy;
	button.setAttribute("aria-busy", String(busy));
}

function showToast(message, type = "") {
	window.clearTimeout(toastTimer);
	elements.toast.textContent = message;
	elements.toast.className = `toast ${type}`.trim();
	elements.toast.hidden = false;
	toastTimer = window.setTimeout(() => {
		elements.toast.hidden = true;
	}, 4200);
}

async function togglePassword(button) {
	const input = document.querySelector(`#${button.dataset.target}`);
	const show = input.type === "password";
	input.type = show ? "text" : "password";
	button.title = show ? "隐藏密码" : "显示密码";
	button.setAttribute("aria-label", button.title);
}

function toggleToken() {
	const show = elements.apiToken.type === "password";
	elements.apiToken.type = show ? "text" : "password";
	const button = document.querySelector("#revealTokenButton");
	button.title = show ? "隐藏 Token" : "显示 Token";
	button.setAttribute("aria-label", button.title);
}

async function copyToken() {
	await copyText(state?.apiToken || "");
	showToast("API Token 已复制", "success");
}

async function copyCurl() {
	await copyText(elements.curlExample.textContent);
	showToast("cURL 示例已复制", "success");
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

function activityItem(title, detail, time) {
	return `
		<div class="activity-item">
			<div class="activity-copy">
				<strong>${escapeHTML(title)}</strong>
				<span>${escapeHTML(detail)}</span>
			</div>
			<span class="activity-time">${escapeHTML(formatRelative(time))}</span>
		</div>
	`;
}

function emptyState(title, detail) {
	return `<div class="activity-empty"><strong>${escapeHTML(title)}</strong><span>${escapeHTML(detail)}</span></div>`;
}

function formatDateTime(value) {
	const date = new Date(value);
	if (!value || Number.isNaN(date.getTime()) || date.getFullYear() < 2020) {
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

function formatRelative(value) {
	const date = new Date(value);
	if (!value || Number.isNaN(date.getTime())) {
		return "—";
	}
	const diff = Date.now() - date.getTime();
	if (diff < 10000) {
		return "刚刚";
	}
	if (diff < 60000) {
		return `${Math.floor(diff / 1000)} 秒前`;
	}
	if (diff < 3600000) {
		return `${Math.floor(diff / 60000)} 分钟前`;
	}
	if (diff < 86400000) {
		return `${Math.floor(diff / 3600000)} 小时前`;
	}
	return formatDateTime(value);
}

function formatDuration(milliseconds) {
	if (!Number.isFinite(milliseconds) || milliseconds < 0) {
		return "—";
	}
	const seconds = Math.floor(milliseconds / 1000);
	const days = Math.floor(seconds / 86400);
	const hours = Math.floor((seconds % 86400) / 3600);
	const minutes = Math.floor((seconds % 3600) / 60);
	if (days > 0) {
		return `${days} 天 ${hours} 小时`;
	}
	if (hours > 0) {
		return `${hours} 小时 ${minutes} 分钟`;
	}
	return `${minutes} 分钟`;
}

function setText(selector, value) {
	const element = document.querySelector(selector);
	if (element) {
		element.textContent = value;
	}
}

function setFieldValue(selector, value, checked = false) {
	const element = document.querySelector(selector);
	if (!element) {
		return;
	}
	if (checked) {
		element.checked = Boolean(value);
		return;
	}
	element.value = value;
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
