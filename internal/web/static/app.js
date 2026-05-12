(function () {
  const cryptoApi = globalThis.crypto;
  const subtle = cryptoApi && cryptoApi.subtle ? cryptoApi.subtle : null;
  const textEncoder = new TextEncoder();
  const textDecoder = new TextDecoder();

  const $ = (id) => document.getElementById(id);
  const els = {
    tabSend: $("tab-send"),
    tabReceive: $("tab-receive"),
    pageSend: $("page-send"),
    pageReceive: $("page-receive"),
    roomId: $("room-id"),
    roomPassword: $("room-password"),
    onlineCount: $("online-count"),
    roomState: $("room-state"),
    shareLink: $("share-link"),
    copyLink: $("copy-link"),
    sendRoomId: $("send-room-id"),
    roomIdHint: $("room-id-hint"),
    sendRoomPassword: $("send-room-password"),
    sendPeerName: $("send-peer-name"),
    createRoom: $("create-room"),
    refreshRoom: $("refresh-room"),
    fileInput: $("file-input"),
    chunkSize: $("chunk-size"),
    uploadBtn: $("upload-btn"),
    toggleUpload: $("toggle-upload"),
    uploadLog: $("upload-log"),
    timeline: $("timeline"),
    receivePeerName: $("receive-peer-name"),
    linkInput: $("link-input"),
    roomInput: $("room-input"),
    passwordInput: $("password-input"),
    joinRoom: $("join-room"),
    downloadLatest: $("download-latest"),
    fileList: $("file-list"),
    downloadLog: $("download-log"),
  };

  const state = {
    roomId: "",
    password: "",
    secret: "",
    snapshot: null,
    ws: null,
    latestTransferId: "",
    manifestCache: {},
    uploads: {},
    downloads: {},
    currentUploadId: "",
  };

  const defaultPeerName = "匿名用户";
  const roomIdPattern = /^[A-Z0-9_-]{4,32}$/;

  function esc(value) {
    return String(value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#39;");
  }

  function fmtTime(value) {
    return new Date(value).toLocaleString("zh-CN", {
      hour12: false,
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  }

  function fmtPct(done, total) {
    return `${Math.min(100, Math.round(((done || 0) / Math.max(total || 1, 1)) * 100))}%`;
  }

  function fmtBytes(size) {
    const units = ["B", "KB", "MB", "GB", "TB"];
    let value = size || 0;
    let idx = 0;
    while (value >= 1024 && idx < units.length - 1) {
      value /= 1024;
      idx += 1;
    }
    return `${value.toFixed(value >= 10 || idx === 0 ? 0 : 1)} ${units[idx]}`;
  }

  function isAbort(error) {
    return error && (error.name === "AbortError" || String(error.message || "").includes("aborted"));
  }

  function needCrypto() {
    if (!cryptoApi || !subtle) {
      throw new Error("当前页面不支持加密，请使用 HTTPS 或 localhost 打开。");
    }
  }

  function peerName() {
    const sendName = (els.sendPeerName.value || "").trim();
    const receiveName = (els.receivePeerName.value || "").trim();
    return sendName || receiveName || defaultPeerName;
  }

  function setPeerName(value) {
    const next = (value || "").trim();
    els.sendPeerName.value = next;
    els.receivePeerName.value = next;
    localStorage.setItem("gossh-peer-name", next);
  }

  function maybeReconnectWS() {
    if (!state.roomId || !state.ws || state.ws.readyState > 1) {
      return;
    }
    connectWS();
  }

  function validateRequestedRoomId(rawValue) {
    const value = (rawValue || "").trim().toUpperCase();
    if (!value) {
      return { ok: true, value: "" };
    }
    if (!roomIdPattern.test(value)) {
      return {
        ok: false,
        value,
        message: "房间号不能少于 4 位；手动填写时必须为 4 到 32 位，只能包含大写字母、数字、下划线或短横线。",
      };
    }
    return { ok: true, value };
  }

  function updateRoomIdHint() {
    const result = validateRequestedRoomId(els.sendRoomId.value);
    if (result.ok) {
      els.roomIdHint.textContent = "房间号可留空自动生成；如果手动填写，必须为 4 到 32 位，只能包含大写字母、数字、下划线或短横线。";
      els.roomIdHint.style.color = "";
      return true;
    }
    els.roomIdHint.textContent = result.message;
    els.roomIdHint.style.color = "var(--accent)";
    return false;
  }

  async function requestJSON(url, options) {
    const response = await fetch(url, options);
    if (!response.ok) {
      throw new Error((await response.text()) || response.statusText);
    }
    return response.json();
  }

  async function requestOK(url, options) {
    const response = await fetch(url, options);
    if (!response.ok) {
      throw new Error((await response.text()) || response.statusText);
    }
    return response;
  }

  function b64url(bytes) {
    let value = "";
    bytes.forEach((b) => {
      value += String.fromCharCode(b);
    });
    return btoa(value).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
  }

  function fromB64url(value) {
    const padded = value.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(value.length / 4) * 4, "=");
    const raw = atob(padded);
    const out = new Uint8Array(raw.length);
    for (let i = 0; i < raw.length; i += 1) {
      out[i] = raw.charCodeAt(i);
    }
    return out;
  }

  async function secretFromPassword(password) {
    needCrypto();
    const digest = await subtle.digest("SHA-256", textEncoder.encode(password));
    return b64url(new Uint8Array(digest));
  }

  async function aesKey(secret) {
    needCrypto();
    return subtle.importKey("raw", fromB64url(secret), "AES-GCM", false, ["encrypt", "decrypt"]);
  }

  async function encrypt(secret, plain) {
    const key = await aesKey(secret);
    const iv = cryptoApi.getRandomValues(new Uint8Array(12));
    const encrypted = await subtle.encrypt({ name: "AES-GCM", iv }, key, plain);
    const out = new Uint8Array(iv.length + encrypted.byteLength);
    out.set(iv);
    out.set(new Uint8Array(encrypted), iv.length);
    return out;
  }

  async function decrypt(secret, cipher) {
    const key = await aesKey(secret);
    const plain = await subtle.decrypt({ name: "AES-GCM", iv: cipher.slice(0, 12) }, key, cipher.slice(12));
    return new Uint8Array(plain);
  }

  async function sha256(bytes) {
    const digest = await subtle.digest("SHA-256", bytes);
    return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
  }

  function randomToken(length) {
    const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
    const bytes = cryptoApi.getRandomValues(new Uint8Array(length));
    return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("");
  }

  function shareUrl() {
    const url = new URL(window.location.href);
    url.hash = `page=receive&room=${encodeURIComponent(state.roomId)}&password=${encodeURIComponent(state.password)}&secret=${encodeURIComponent(state.secret)}`;
    return url.toString();
  }

  function parseHash(hash) {
    const params = new URLSearchParams((hash || window.location.hash).replace(/^#/, ""));
    return {
      page: params.get("page"),
      room: params.get("room"),
      password: params.get("password"),
      secret: params.get("secret"),
    };
  }

  function tryParseLink(value) {
    try {
      return parseHash(new URL(value).hash);
    } catch {
      return null;
    }
  }

  function switchPage(page) {
    const isSend = page === "send";
    els.pageSend.classList.toggle("active", isSend);
    els.pageReceive.classList.toggle("active", !isSend);
    els.tabSend.classList.toggle("active", isSend);
    els.tabSend.classList.toggle("ghost", !isSend);
    els.tabReceive.classList.toggle("active", !isSend);
    els.tabReceive.classList.toggle("ghost", isSend);
  }

  function copyFeedback(kind, text) {
    const base = els.copyLink.dataset.base || "复制房间链接";
    els.copyLink.dataset.base = base;
    els.copyLink.textContent = text;
    els.copyLink.classList.remove("copy-success", "copy-error");
    els.copyLink.classList.add(kind === "success" ? "copy-success" : "copy-error");
    clearTimeout(els.copyLink._timer);
    els.copyLink._timer = window.setTimeout(() => {
      els.copyLink.textContent = base;
      els.copyLink.classList.remove("copy-success", "copy-error");
    }, 1200);
  }

  function eventTypeLabel(type) {
    const labels = {
      "room.created": "房间创建",
      "peer.joined": "进入房间",
      "peer.left": "离开房间",
      "transfer.created": "准备发送",
      "transfer.started": "开始上传",
      "transfer.ready": "上传完成",
      "transfer.downloading": "开始下载",
      "transfer.deleted": "删除文件",
    };
    return labels[type] || type;
  }

  function transferStatusLabel(status) {
    const labels = {
      pending: "等待上传",
      uploading: "上传中",
      ready: "可下载",
      downloading: "下载中",
    };
    return labels[status] || status;
  }

  function updateButtons() {
    els.uploadBtn.disabled = !(state.roomId && state.secret && els.fileInput.files[0] && !state.currentUploadId);
    els.toggleUpload.disabled = !state.currentUploadId;
    els.copyLink.disabled = !(state.roomId && state.secret);
  }

  function updateSummary() {
    els.roomId.textContent = state.roomId || "未进入";
    els.roomPassword.textContent = state.password || "未设置";
    els.onlineCount.textContent = String((state.snapshot && state.snapshot.online) || 0);
    els.roomState.textContent = (state.snapshot && state.snapshot.room && state.snapshot.room.status) || "未进入";
    els.shareLink.value = state.roomId && state.secret ? shareUrl() : "";
    updateButtons();
  }

  function syncRoomInputs() {
    els.sendRoomId.value = state.roomId || els.sendRoomId.value;
    els.sendRoomPassword.value = state.password || els.sendRoomPassword.value;
    els.roomInput.value = state.roomId || "";
    els.passwordInput.value = state.password || "";
  }

  function renderEvents(events) {
    if (!events || events.length === 0) {
      els.timeline.innerHTML = '<div class="empty">当前房间还没有动态。</div>';
      return;
    }
    els.timeline.innerHTML = events
      .slice()
      .reverse()
      .map((event) => {
        const category = event.type.startsWith("transfer") ? "transfer" : "system";
        return `<article class="event-card ${category}"><div class="event-head"><strong>${esc(event.actor || "系统消息")}</strong><span class="event-type">${esc(eventTypeLabel(event.type))}</span></div><div class="event-body"><div>${esc(event.message || "房间状态更新")}</div><div class="timeline-time">${esc(fmtTime(event.createdAt))}</div></div></article>`;
      })
      .join("");
  }

  function currentUploadProgress(transfer) {
    const upload = state.uploads[transfer.id];
    if (!upload) {
      return fmtPct(transfer.uploadedChunks || 0, transfer.totalChunks || 0);
    }
    return fmtPct(upload.done, upload.total);
  }

  function downloadButtonLabel(transferId) {
    const download = state.downloads[transferId];
    if (!download) {
      return "下载文件";
    }
    return download.paused ? `继续下载 ${download.percent}` : `下载中 ${download.percent}`;
  }

  function updateDownloadLatestButton() {
    if (!state.latestTransferId) {
      els.downloadLatest.disabled = true;
      els.downloadLatest.textContent = "下载最新文件";
      return;
    }
    els.downloadLatest.disabled = false;
    els.downloadLatest.textContent = downloadButtonLabel(state.latestTransferId);
  }

  function renderFiles(transfers) {
    if (!transfers || transfers.length === 0) {
      state.latestTransferId = "";
      updateDownloadLatestButton();
      els.fileList.innerHTML = '<div class="empty">当前房间还没有文件。</div>';
      return;
    }

    const latest = transfers.slice().reverse().find((transfer) => transfer.status === "ready" || transfer.status === "downloading");
    state.latestTransferId = latest ? latest.id : "";
    updateDownloadLatestButton();

    els.fileList.innerHTML = transfers
      .slice()
      .reverse()
      .map((transfer) => {
        const manifest = state.manifestCache[transfer.id] || {};
        const download = state.downloads[transfer.id];
        const upload = state.uploads[transfer.id];
        const progress = upload ? currentUploadProgress(transfer) : fmtPct(transfer.uploadedChunks || 0, transfer.totalChunks || 0);
        const downloadToggleLabel = download ? (download.paused ? "继续下载" : "暂停下载") : "暂停下载";
        const size = manifest.size ? fmtBytes(manifest.size) : fmtBytes(transfer.bytesReceived);
        return `<article class="file-card"><div class="file-head"><strong>${esc(manifest.name || transfer.id)}</strong><span class="file-status ${esc(transfer.status)}">${esc(transferStatusLabel(transfer.status))}</span></div><div class="file-body"><div class="file-sub">${esc(transfer.sender)} · ${esc(fmtTime(transfer.createdAt))}</div><div class="file-meta"><span>大小：${esc(size)}</span><span>分块：${esc(String(transfer.totalChunks || 0))}</span><span>上传：${esc(progress)}</span></div><div class="file-actions"><button class="download-btn-inline" data-id="${esc(transfer.id)}" type="button" ${(transfer.status === "ready" || transfer.status === "downloading") ? "" : "disabled"}>${esc(downloadButtonLabel(transfer.id))}</button><button class="toggle-download-btn ghost" data-id="${esc(transfer.id)}" type="button" ${download ? "" : "disabled"}>${esc(downloadToggleLabel)}</button><button class="delete-transfer-btn ghost" data-id="${esc(transfer.id)}" type="button">删除文件</button></div></div></article>`;
      })
      .join("");

    document.querySelectorAll(".download-btn-inline").forEach((button) => {
      button.onclick = () => {
        const id = button.dataset.id;
        const download = state.downloads[id];
        const action = download && download.paused ? resumeDownload(id) : downloadTransfer(id);
        action.catch((error) => {
          if (!isAbort(error)) {
            els.downloadLog.textContent = error.message;
          }
        });
      };
    });

    document.querySelectorAll(".toggle-download-btn").forEach((button) => {
      button.onclick = () => {
        const id = button.dataset.id;
        const download = state.downloads[id];
        if (!download) {
          return;
        }
        const action = download.paused ? resumeDownload(id) : Promise.resolve(pauseDownload(id));
        action.catch((error) => {
          if (!isAbort(error)) {
            els.downloadLog.textContent = error.message;
          }
        });
      };
    });

    document.querySelectorAll(".delete-transfer-btn").forEach((button) => {
      button.onclick = () => {
        deleteTransfer(button.dataset.id).catch((error) => {
          els.downloadLog.textContent = error.message;
        });
      };
    });
  }

  async function hydrateManifests(transfers) {
    if (!state.secret) {
      return;
    }
    let changed = false;
    for (const transfer of transfers || []) {
      if (state.manifestCache[transfer.id] || transfer.manifestSize <= 0) {
        continue;
      }
      try {
        const response = await requestOK(`/api/v1/rooms/${state.roomId}/transfers/${transfer.id}/manifest`);
        const plain = await decrypt(state.secret, new Uint8Array(await response.arrayBuffer()));
        state.manifestCache[transfer.id] = JSON.parse(textDecoder.decode(plain));
        changed = true;
      } catch {
      }
    }
    if (changed && state.snapshot) {
      renderFiles(state.snapshot.transfers || []);
    }
  }

  async function refreshRoom() {
    if (!state.roomId) {
      return;
    }
    state.snapshot = await requestJSON(`/api/v1/rooms/${state.roomId}`);
    updateSummary();
    syncRoomInputs();
    renderEvents(state.snapshot.events || []);
    renderFiles(state.snapshot.transfers || []);
    hydrateManifests(state.snapshot.transfers || []);
  }

  function connectWS() {
    if (!state.roomId) {
      return;
    }
    const protocol = window.location.protocol === "https:" ? "wss" : "ws";
    if (state.ws) {
      state.ws.close();
    }
    state.ws = new WebSocket(`${protocol}://${window.location.host}/ws/rooms/${state.roomId}?peer=${encodeURIComponent(peerName())}`);
    state.ws.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data);
        if (payload.type === "room.snapshot" && payload.data) {
          state.snapshot = payload.data;
          updateSummary();
          renderEvents(payload.data.events || []);
          renderFiles(payload.data.transfers || []);
          hydrateManifests(payload.data.transfers || []);
          return;
        }
      } catch {
      }
      refreshRoom().catch(() => {});
    };
    state.ws.onclose = () => {
      state.ws = null;
    };
  }

  async function createRoom() {
    needCrypto();
    const roomIdCheck = validateRequestedRoomId(els.sendRoomId.value);
    if (!roomIdCheck.ok) {
      updateRoomIdHint();
      throw new Error(roomIdCheck.message);
    }
    const requestedRoomId = roomIdCheck.value;
    const password = (els.sendRoomPassword.value || "").trim() || randomToken(10);
    setPeerName(peerName());
    const response = await requestJSON(`/api/v1/rooms${requestedRoomId ? `?roomId=${encodeURIComponent(requestedRoomId)}` : ""}`, {
      method: "POST",
    });

    state.roomId = response.room.id;
    state.password = password;
    state.secret = await secretFromPassword(password);
    state.snapshot = null;
    syncRoomInputs();
    updateSummary();
    switchPage("send");
    connectWS();
    await refreshRoom();
    els.uploadLog.textContent = `房间已创建：${state.roomId}`;
  }

  async function enterReceiveRoom() {
    needCrypto();
    setPeerName(peerName());
    const parsed = els.linkInput.value.trim() ? tryParseLink(els.linkInput.value.trim()) : null;
    const roomId = ((parsed && parsed.room) || els.roomInput.value || "").trim().toUpperCase();
    const password = ((parsed && parsed.password) || els.passwordInput.value || "").trim();
    const secret = parsed && parsed.secret ? parsed.secret : (password ? await secretFromPassword(password) : "");

    if (!roomId) {
      throw new Error("请输入房间号，或者粘贴完整分享链接。");
    }

    state.roomId = roomId;
    state.password = password;
    state.secret = secret;
    state.snapshot = null;
    syncRoomInputs();
    updateSummary();
    switchPage("receive");
    connectWS();
    await refreshRoom();
    els.downloadLog.textContent = `已进入房间：${state.roomId}`;
  }

  async function startUpload() {
    needCrypto();
    if (!state.roomId || !state.secret) {
      throw new Error("请先创建发送房间。");
    }
    const file = els.fileInput.files[0];
    if (!file) {
      throw new Error("请先选择文件。");
    }

    const sender = encodeURIComponent(peerName());
    const transfer = await requestJSON(`/api/v1/rooms/${state.roomId}/transfers?sender=${sender}`, { method: "POST" });
    const chunkSize = Number(els.chunkSize.value);
    const totalChunks = Math.ceil(file.size / chunkSize);

    state.manifestCache[transfer.id] = {
      name: file.name,
      size: file.size,
      type: file.type || "application/octet-stream",
      lastModified: file.lastModified,
      chunkSize,
      totalChunks,
    };
    state.currentUploadId = transfer.id;
    state.uploads[transfer.id] = {
      file,
      chunkSize,
      total: totalChunks,
      done: 0,
      paused: false,
      controller: null,
      manifestUploaded: false,
    };
    els.toggleUpload.textContent = "暂停上传";
    updateButtons();
    await continueUpload(transfer.id);
  }

  async function continueUpload(transferId) {
    const upload = state.uploads[transferId];
    if (!upload) {
      return;
    }

    if (!upload.manifestUploaded) {
      const body = await encrypt(state.secret, textEncoder.encode(JSON.stringify(state.manifestCache[transferId])));
      await requestOK(`/api/v1/rooms/${state.roomId}/transfers/${transferId}/manifest?chunkSize=${upload.chunkSize}&totalChunks=${upload.total}`, {
        method: "PUT",
        headers: { "content-type": "application/octet-stream" },
        body,
      });
      upload.manifestUploaded = true;
    }

    const resumeState = await requestJSON(`/api/v1/rooms/${state.roomId}/transfers/${transferId}/resume`);
    const uploadedChunks = new Set(resumeState.uploadedChunks || []);
    upload.done = uploadedChunks.size;
    renderFiles((state.snapshot && state.snapshot.transfers) || []);

    for (let index = 0; index < upload.total; index += 1) {
      if (upload.paused) {
        throw new DOMException("Upload paused", "AbortError");
      }
      if (uploadedChunks.has(index)) {
        continue;
      }

      const start = index * upload.chunkSize;
      const end = Math.min(upload.file.size, (index + 1) * upload.chunkSize);
      const plain = new Uint8Array(await upload.file.slice(start, end).arrayBuffer());
      const cipher = await encrypt(state.secret, plain);

      upload.controller = new AbortController();
      await requestOK(`/api/v1/rooms/${state.roomId}/transfers/${transferId}/chunks/${index}`, {
        method: "PUT",
        headers: {
          "content-type": "application/octet-stream",
          "x-checksum-sha256": await sha256(cipher),
        },
        body: cipher,
        signal: upload.controller.signal,
      });
      upload.controller = null;
      upload.done += 1;
      els.uploadLog.textContent = `上传中 ${fmtPct(upload.done, upload.total)}`;
      renderFiles((state.snapshot && state.snapshot.transfers) || []);
    }

    delete state.uploads[transferId];
    state.currentUploadId = "";
    els.toggleUpload.textContent = "暂停上传";
    els.uploadLog.textContent = `发送完成：${state.manifestCache[transferId].name}`;
    updateButtons();
    await refreshRoom();
  }

  function pauseUpload() {
    const upload = state.uploads[state.currentUploadId];
    if (!upload) {
      return;
    }
    upload.paused = true;
    if (upload.controller) {
      upload.controller.abort();
    }
    els.toggleUpload.textContent = "继续上传";
    els.uploadLog.textContent = `已暂停 ${fmtPct(upload.done, upload.total)}`;
    renderFiles((state.snapshot && state.snapshot.transfers) || []);
  }

  async function resumeUpload() {
    const upload = state.uploads[state.currentUploadId];
    if (!upload) {
      return;
    }
    upload.paused = false;
    els.toggleUpload.textContent = "暂停上传";
    await continueUpload(state.currentUploadId);
  }

  async function downloadTransfer(transferId) {
    if (!state.secret) {
      throw new Error("缺少房间密码，无法解密文件。");
    }

    if (!state.downloads[transferId]) {
      state.downloads[transferId] = {
        paused: false,
        percent: "0%",
        controller: null,
        parts: [],
        next: 0,
        manifest: null,
      };
    }

    const download = state.downloads[transferId];
    if (!download.manifest) {
      const response = await requestOK(`/api/v1/rooms/${state.roomId}/transfers/${transferId}/manifest`);
      const plain = await decrypt(state.secret, new Uint8Array(await response.arrayBuffer()));
      download.manifest = JSON.parse(textDecoder.decode(plain));
      state.manifestCache[transferId] = download.manifest;
    }

    await continueDownload(transferId);
  }

  async function continueDownload(transferId) {
    const download = state.downloads[transferId];
    const manifest = download.manifest;

    for (let index = download.next; index < manifest.totalChunks; index += 1) {
      if (download.paused) {
        throw new DOMException("Download paused", "AbortError");
      }
      download.controller = new AbortController();
      const response = await requestOK(`/api/v1/rooms/${state.roomId}/transfers/${transferId}/chunks/${index}?actor=${encodeURIComponent(peerName())}`, {
        signal: download.controller.signal,
      });
      const cipher = new Uint8Array(await response.arrayBuffer());
      download.parts.push(await decrypt(state.secret, cipher));
      download.controller = null;
      download.next = index + 1;
      download.percent = fmtPct(index + 1, manifest.totalChunks);
      els.downloadLog.textContent = `下载中 ${download.percent}`;
      renderFiles((state.snapshot && state.snapshot.transfers) || []);
    }

    const blob = new Blob(download.parts, { type: manifest.type || "application/octet-stream" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = manifest.name;
    anchor.click();
    URL.revokeObjectURL(url);

    delete state.downloads[transferId];
    updateDownloadLatestButton();
    els.downloadLog.textContent = `下载完成：${manifest.name}`;
    renderFiles((state.snapshot && state.snapshot.transfers) || []);
    await refreshRoom();
  }

  function pauseDownload(transferId) {
    const download = state.downloads[transferId];
    if (!download) {
      return;
    }
    download.paused = true;
    if (download.controller) {
      download.controller.abort();
    }
    els.downloadLog.textContent = `已暂停 ${download.percent}`;
    renderFiles((state.snapshot && state.snapshot.transfers) || []);
  }

  async function resumeDownload(transferId) {
    const download = state.downloads[transferId];
    if (!download) {
      return;
    }
    download.paused = false;
    await continueDownload(transferId);
  }

  async function deleteTransfer(transferId) {
    const download = state.downloads[transferId];
    if (download && download.controller) {
      download.controller.abort();
    }
    const upload = state.uploads[transferId];
    if (upload && upload.controller) {
      upload.controller.abort();
    }

    await requestOK(`/api/v1/rooms/${state.roomId}/transfers/${transferId}?actor=${encodeURIComponent(peerName())}`, {
      method: "DELETE",
    });

    delete state.manifestCache[transferId];
    delete state.downloads[transferId];
    delete state.uploads[transferId];

    if (state.currentUploadId === transferId) {
      state.currentUploadId = "";
      els.toggleUpload.textContent = "暂停上传";
      els.uploadLog.textContent = "当前上传已删除。";
    }
    updateButtons();
    await refreshRoom();
  }

  els.tabSend.onclick = () => switchPage("send");
  els.tabReceive.onclick = () => switchPage("receive");
  els.createRoom.onclick = () => {
    createRoom().catch((error) => {
      els.uploadLog.textContent = error.message;
    });
  };
  els.refreshRoom.onclick = () => {
    refreshRoom().catch((error) => {
      els.uploadLog.textContent = error.message;
    });
  };
  els.joinRoom.onclick = () => {
    enterReceiveRoom().catch((error) => {
      els.downloadLog.textContent = error.message;
    });
  };
  els.copyLink.onclick = async () => {
    if (!els.shareLink.value) {
      return;
    }
    try {
      await navigator.clipboard.writeText(els.shareLink.value);
      copyFeedback("success", "已复制链接");
    } catch {
      copyFeedback("error", "复制失败");
    }
  };
  els.fileInput.onchange = updateButtons;
  els.sendRoomId.addEventListener("input", () => {
    els.sendRoomId.value = els.sendRoomId.value.toUpperCase();
    updateRoomIdHint();
  });
  els.uploadBtn.onclick = () => {
    startUpload().catch((error) => {
      if (!isAbort(error)) {
        els.uploadLog.textContent = error.message;
      }
    });
  };
  els.toggleUpload.onclick = () => {
    if (!state.currentUploadId) {
      return;
    }
    const upload = state.uploads[state.currentUploadId];
    const action = upload && upload.paused ? resumeUpload() : Promise.resolve(pauseUpload());
    action.catch((error) => {
      if (!isAbort(error)) {
        els.uploadLog.textContent = error.message;
      }
    });
  };
  els.downloadLatest.onclick = () => {
    if (!state.latestTransferId) {
      return;
    }
    const download = state.downloads[state.latestTransferId];
    const action = download && download.paused ? resumeDownload(state.latestTransferId) : downloadTransfer(state.latestTransferId);
    action.catch((error) => {
      if (!isAbort(error)) {
        els.downloadLog.textContent = error.message;
      }
    });
  };

  [els.sendPeerName, els.receivePeerName].forEach((input) => {
    input.addEventListener("change", () => {
      setPeerName(input.value);
      maybeReconnectWS();
    });
    input.addEventListener("blur", () => {
      setPeerName(input.value);
    });
  });

  (async function boot() {
    needCrypto();
    const savedName = localStorage.getItem("gossh-peer-name");
    if (savedName) {
      setPeerName(savedName);
    }

    renderEvents([]);
    renderFiles([]);
    updateSummary();
    updateDownloadLatestButton();
    updateRoomIdHint();

    const parsed = parseHash();
    if (parsed.page === "receive") {
      switchPage("receive");
    }
    if (parsed.room) {
      state.roomId = parsed.room.toUpperCase();
      state.password = parsed.password || "";
      state.secret = parsed.secret || (state.password ? await secretFromPassword(state.password) : "");
      syncRoomInputs();
      updateSummary();
      connectWS();
      refreshRoom().catch((error) => {
        els.downloadLog.textContent = error.message;
      });
    }

    window.setInterval(() => {
      if (state.roomId) {
        refreshRoom().catch(() => {});
      }
    }, 15000);
  })().catch((error) => {
    els.uploadLog.textContent = error.message;
    els.downloadLog.textContent = error.message;
  });
})();
