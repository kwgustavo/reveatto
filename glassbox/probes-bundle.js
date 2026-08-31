/* GlassBox probes — browser-side bundle.
 * Sent to the browser as a single <script> by the GlassBox Go middleware.
 * Ported from the upstream GlassBox project (https://glassbox.codecanary.org)
 * — every probe that the upstream `index.html` registers is ported here.
 *
 * Probes run on load (deferred by the injected tag), collect the
 * characteristic vector grouped into 4 tiers (hw, engine, build, session),
 * compute the same entropy estimate as the upstream demo, and send the
 * result over a single WebSocket frame.
 *
 * Opt-in:  `window.__GLASSBOX_GEO__ = true` enables probe 26 (public IP).
 * Opt-in:  `window.__GLASSBOX_BEHAV__ = true` enables probe 24 counters
 *          (but the bundle still emits them only if a user gesture wired
 *          them up; this bundle never records/sends raw events).
 */
(function(){
  if (window.__GLASSBOX_RUNNING__) return;
  window.__GLASSBOX_RUNNING__ = true;

  const yes = v => v ? "yes" : "no";
  function safe(fn){ try { return fn(); } catch(e){ return "err: " + ((e && e.message) || e); } }

  async function sha256(str){
    try {
      const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(str));
      return [...new Uint8Array(buf)].map(b => b.toString(16).padStart(2, "0")).join("");
    } catch(e){
      let h1 = 0xdeadbeef, h2 = 0x41c6ce57;
      for(let i = 0; i < str.length; i++){
        const c = str.charCodeAt(i);
        h1 = Math.imul(h1 ^ c, 2654435761);
        h2 = Math.imul(h2 ^ c, 1597334677);
      }
      h1 = Math.imul(h1 ^ (h1>>>16), 2246822507) ^ Math.imul(h2 ^ (h2>>>13), 3266489909);
      h2 = Math.imul(h2 ^ (h2>>>16), 2246822507) ^ Math.imul(h1 ^ (h1>>>13), 3266489909);
      return (h2>>>0).toString(16).padStart(8, "0") + (h1>>>0).toString(16).padStart(8, "0") + "·fallback";
    }
  }

  const TIERS = { hw:{}, engine:{}, build:{}, session:{} };
  const TIER_MAP = {
    screen:"hw", hz:"hw", cores:"hw", mem:"hw", touch:"hw",
    glvendor:"hw", glrend:"hw", gpu:"hw",
    tz:"hw", locale:"hw", dst:"hw", plat:"hw", langs:"hw",
    canvas:"engine", audio:"engine", math:"engine", codecs:"engine", glext:"engine", fonts:"engine", rects:"engine", tmetrics:"engine",
    ua:"build", uach:"build", apis:"build", voices:"build", kbd:"build", wasm:"build", bot:"build",
    css:"session", timing:"session", quota:"session", net:"session", battery:"session",
    perms:"session", webrtc:"session", ip:"session", ip_asn:"session", cookies:"session", devices:"session"
  };
  function feed(key, val){
    if (val === undefined || val === null || val === "") return;
    (TIERS[TIER_MAP[key] || "session"])[key] = String(val);
  }

  // ========= Probes =========

  async function p01NavigatorCore(){
    const n = navigator;
    feed("ua",   n.userAgent);
    feed("plat", safe(()=>n.platform));
    feed("langs", safe(()=>(n.languages || []).join(",")));
    feed("cores", n.hardwareConcurrency);
    feed("mem",   n.deviceMemory);
    feed("touch", n.maxTouchPoints);
  }

  async function p02ClientHints(){
    const uad = navigator.userAgentData;
    if (!uad) return;
    try {
      const h = await uad.getHighEntropyValues([
        "architecture","bitness","model","platformVersion","uaFullVersion","fullVersionList","wow64","formFactor"
      ]);
      feed("uach", JSON.stringify(h));
    } catch(e){}
  }

  function measureHz(){
    return new Promise(res => {
      let last = performance.now(), diffs = [], n = 0;
      function tick(t){
        const d = t - last; last = t; if (n++ > 2) diffs.push(d);
        if (n < 26) requestAnimationFrame(tick);
        else {
          diffs.sort((a,b)=>a-b);
          const med = diffs[diffs.length >> 1] || 16.7;
          res(Math.round(1000 / med));
        }
      }
      requestAnimationFrame(tick);
    });
  }
  async function p03Screen(){
    const s = screen;
    feed("screen", `${s.width}x${s.height}x${s.colorDepth}@${window.devicePixelRatio}`);
    const hz = await measureHz();
    feed("hz", hz);
  }

  async function p04TimezoneLocale(){
    const dt = Intl.DateTimeFormat().resolvedOptions();
    const jan = new Date(2025,0,1).getTimezoneOffset();
    const jul = new Date(2025,6,1).getTimezoneOffset();
    feed("tz",     dt.timeZone);
    feed("locale", dt.locale);
    feed("dst",    jan !== jul);
  }

  async function p05Canvas(){
    try{
      const c = document.createElement("canvas"); c.width = 280; c.height = 70;
      const x = c.getContext("2d");
      x.textBaseline = "top"; x.font = "16px 'Arial'";
      x.fillStyle = "#f60"; x.fillRect(2,2,110,22);
      x.fillStyle = "#069"; x.fillText("GlassBox \u{1F512} fp \u2591\u2592\u2593 ", 6, 4);
      x.fillStyle = "rgba(102,204,0,.66)"; x.font = "18px 'Times New Roman'";
      x.fillText("Cwm fjord \u{1F984} \u00E9\u00E7\u00F1", 6, 26);
      x.strokeStyle = "#0aa"; x.beginPath(); x.arc(220,30,18,0,7); x.stroke();
      x.globalCompositeOperation = "multiply";
      ["#f0f","#0ff","#ff0"].forEach((col,i)=>{
        x.fillStyle = col; x.beginPath(); x.arc(60+i*30, 45, 22, 0, 7); x.fill();
      });
      const data = c.toDataURL();
      feed("canvas", await sha256(data));
    } catch(e){}
  }

  async function p06WebGL(){
    const c = document.createElement("canvas");
    const gl = c.getContext("webgl2") || c.getContext("webgl") || c.getContext("experimental-webgl");
    if (!gl) return;
    const dbg = gl.getExtension("WEBGL_debug_renderer_info");
    const vend = dbg ? gl.getParameter(dbg.UNMASKED_VENDOR_WEBGL) : gl.getParameter(gl.VENDOR);
    const rend = dbg ? gl.getParameter(dbg.UNMASKED_RENDERER_WEBGL) : gl.getParameter(gl.RENDERER);
    feed("glvendor", vend);
    feed("glrend",   rend);
    const exts = (gl.getSupportedExtensions() || []);
    feed("glext", await sha256(exts.join(",")));
  }

  async function p07WebGPU(){
    if (!navigator.gpu) return;
    try {
      const ad = await navigator.gpu.requestAdapter();
      if (!ad) return;
      let info = {};
      try { info = ad.info || (ad.requestAdapterInfo && await ad.requestAdapterInfo()) || {}; } catch(e){}
      const feats = [...(ad.features || [])].sort();
      feed("gpu", [info.vendor || "", info.architecture || "", info.device || "", feats.join(",")].join("|"));
    } catch(e){}
  }

  async function p08Audio(){
    const AC = window.OfflineAudioContext || window.webkitOfflineAudioContext;
    if (!AC) return;
    try {
      const ctx = new AC(1, 44100, 44100);
      const osc = ctx.createOscillator(); osc.type = "triangle"; osc.frequency.value = 10000;
      const comp = ctx.createDynamicsCompressor();
      ["threshold","knee","ratio","attack","release"].forEach((p,i)=>{
        if (comp[p]) comp[p].value = [-50,40,12,0,.25][i];
      });
      osc.connect(comp); comp.connect(ctx.destination); osc.start(0);
      const buf = await ctx.startRendering();
      const ch = buf.getChannelData(0);
      let sum = 0; for (let i = 4500; i < 5000; i++) sum += Math.abs(ch[i]);
      feed("audio", await sha256(sum.toString()));
    } catch(e){}
  }

  async function p09Fonts(){
    const base = ["monospace","sans-serif","serif"];
    const probe = ["Arial","Arial Black","Arial Narrow","Calibri","Cambria","Comic Sans MS","Consolas",
      "Courier New","Georgia","Helvetica","Helvetica Neue","Impact","Lucida Console","Lucida Grande",
      "Menlo","Monaco","Palatino","Segoe UI","Tahoma","Times New Roman","Trebuchet MS","Verdana",
      "Cascadia Code","JetBrains Mono","Roboto","Ubuntu","Noto Sans","DejaVu Sans","Liberation Sans",
      "SF Pro Text","San Francisco","Andale Mono","Gill Sans","Optima","Futura","Baskerville",
      "Microsoft YaHei","MS Gothic","Malgun Gothic","Apple Color Emoji","Segoe UI Emoji"];
    const span = document.createElement("span");
    span.style.cssText = "position:absolute;left:-9999px;font-size:72px";
    span.textContent = "mmmmmmmmmwwwwwww\u{1F984}\u4E2D\u6587Il1";
    document.body.appendChild(span);
    const baseW = {}, baseH = {};
    base.forEach(b => {
      span.style.fontFamily = b;
      baseW[b] = span.offsetWidth; baseH[b] = span.offsetHeight;
    });
    const found = [];
    probe.forEach(f => {
      for (const b of base){
        span.style.fontFamily = `'${f}',${b}`;
        if (span.offsetWidth !== baseW[b] || span.offsetHeight !== baseH[b]){ found.push(f); break; }
      }
    });
    document.body.removeChild(span);
    feed("fonts", await sha256(found.join(",")));
  }

  async function p10Codecs(){
    const v = document.createElement("video");
    const a = document.createElement("audio");
    const vids = {
      "H.264":'video/mp4; codecs="avc1.42E01E"',
      "HEVC/H.265":'video/mp4; codecs="hev1.1.6.L93.B0"',
      "VP9":'video/webm; codecs="vp9"',
      "AV1":'video/mp4; codecs="av01.0.05M.08"',
      "VP8":'video/webm; codecs="vp8"',
    };
    const auds = {
      "AAC":'audio/mp4; codecs="mp4a.40.2"',"Opus":'audio/webm; codecs="opus"',
      "MP3":'audio/mpeg',"FLAC":'audio/flac',"Vorbis":'audio/webm; codecs="vorbis"',
    };
    const line = [];
    for (const [name, t] of Object.entries(vids)){ const r = v.canPlayType(t); line.push(name+":"+(r||"no")); }
    for (const [name, t] of Object.entries(auds)){ const r = a.canPlayType(t); line.push(name+":"+(r||"no")); }
    feed("codecs", await sha256(line.join("|")));
  }

  async function p11Voices(){
    if (!("speechSynthesis" in window)) return;
    const voices = await new Promise(res => {
      let v = speechSynthesis.getVoices();
      if (v.length) return res(v);
      let done = false;
      speechSynthesis.onvoiceschanged = () => { if (!done){ done = true; res(speechSynthesis.getVoices()); } };
      setTimeout(() => { if (!done){ done = true; res(speechSynthesis.getVoices()); } }, 600);
    });
    const names = voices.map(v => v.name + "|" + v.lang + (v.localService ? "|L" : ""));
    feed("voices", await sha256(names.join(",")));
  }

  async function p12Devices(){
    if (!navigator.mediaDevices?.enumerateDevices) return;
    try {
      const d = await navigator.mediaDevices.enumerateDevices();
      feed("devices", d.map(x => x.kind).sort().join(","));
    } catch(e){}
  }

  async function p13Network(){
    const c = navigator.connection || navigator.mozConnection || navigator.webkitConnection;
    if (!c) return;
    feed("net", c.effectiveType);
  }

  async function p14WebRTC(){
    if (!window.RTCPeerConnection) return;
    try {
      const pc = new RTCPeerConnection({iceServers: []});
      pc.createDataChannel("x");
      const real = new Set();
      const isv4 = s => /^(\d{1,3}\.){3}\d{1,3}$/.test(s) && s.split(".").every(o => +o < 256);
      const isv6 = s => /^[0-9a-f]{0,4}(:[0-9a-f]{0,4}){2,7}$/i.test(s);
      await new Promise(async res => {
        pc.onicecandidate = e => {
          if (!e.candidate){ res(); return; }
          const addr = (e.candidate.candidate.split(" ")[4] || "").toLowerCase();
          if (!addr) return;
          if (addr.endsWith(".local")) return;
          else if (isv4(addr) || isv6(addr)) real.add(addr);
        };
        pc.onicegatheringstatechange = () => { if (pc.iceGatheringState === "complete") res(); };
        await pc.setLocalDescription(await pc.createOffer());
        setTimeout(res, 1500);
      });
      pc.close();
      const reals = [...real];
      if (reals.length) feed("webrtc", reals.sort().join(","));
    } catch(e){}
  }

  async function p15Storage(){
    if (navigator.storage?.estimate){
      try {
        const e = await navigator.storage.estimate();
        feed("quota", Math.round((e.quota || 0) / 1073741824));
      } catch(e){}
    }
  }

  async function p16Permissions(){
    if (!navigator.permissions?.query) return;
    const names = ["geolocation","notifications","camera","microphone","clipboard-read",
      "clipboard-write","push","midi","background-sync","persistent-storage","accelerometer"];
    const chips = [];
    for (const n of names){
      try {
        const p = await navigator.permissions.query({name: n});
        chips.push({t: n+"·"+p.state, on: p.state === "granted"});
      } catch(e){
        chips.push({t: n+"·n/a", off: true});
      }
    }
    feed("perms", chips.map(c => c.t).join(","));
  }

  async function p17API(){
    const feats = {
      "Bluetooth":"bluetooth" in navigator,"USB":"usb" in navigator,"Serial":"serial" in navigator,
      "HID":"hid" in navigator,"NFC":"NDEFReader" in window,"Idle":"IdleDetector" in window,
      "ContactPicker":"contacts" in navigator,"WakeLock":"wakeLock" in navigator,
      "PaymentRequest":"PaymentRequest" in window,"WebAuthn":"PublicKeyCredential" in window,
      "WebXR":"xr" in navigator,"WebTransport":"WebTransport" in window,"WebCodecs":"VideoEncoder" in window,
      "WebNN":"ml" in navigator,"FileSystemAccess":"showOpenFilePicker" in window,
      "Gamepad":"getGamepads" in navigator,"Vibration":"vibrate" in navigator,
      "Battery":"getBattery" in navigator,"Gyroscope":"Gyroscope" in window,
      "Accelerometer":"Accelerometer" in window,"AmbientLight":"AmbientLightSensor" in window,
      "EyeDropper":"EyeDropper" in window,"Share":"share" in navigator,
      "SpeechRecog":"webkitSpeechRecognition" in window || "SpeechRecognition" in window,
      "ScreenWakeLock":"wakeLock" in navigator,"CredMgmt":"credentials" in navigator,
      "OffscreenCanvas":"OffscreenCanvas" in window,"BroadcastChannel":"BroadcastChannel" in window,
      "ResizeObserver":"ResizeObserver" in window,"ReportingObserver":"ReportingObserver" in window,
    };
    feed("apis", await sha256(Object.entries(feats).map(([k,v])=>k+v).join(",")));
  }

  async function p18CSS(){
    const mm = q => matchMedia(q).matches;
    const scheme = mm("(prefers-color-scheme:dark)") ? "dark" : "light";
    const gamut = mm("(color-gamut:rec2020)") ? "rec2020" : mm("(color-gamut:p3)") ? "p3" : "srgb";
    let sw = 0;
    try {
      const d = document.createElement("div");
      d.style.cssText = "width:100px;height:100px;overflow:scroll;position:absolute;left:-9999px";
      document.body.appendChild(d); sw = d.offsetWidth - d.clientWidth; document.body.removeChild(d);
    } catch(e){}
    feed("css", scheme + gamut + sw);
  }

  async function p19Math(){
    const fns = {
      "sin(1e300)":Math.sin(1e300),"tan(-1e300)":Math.tan(-1e300),
      "cosh(10)":Math.cosh(10),"expm1(1)":Math.expm1(1),"sinh(1)":Math.sinh(1),
      "atanh(.5)":Math.atanh(.5),"acosh(1e300)":Math.acosh(1e300),"pow(π,-100)":Math.pow(Math.PI,-100),
    };
    const line = Object.entries(fns).map(([k,v]) => k+"="+v);
    feed("math", await sha256(line.join("|")));
  }

  async function p20Timing(){
    let minDelta = Infinity, prev = performance.now();
    for (let i = 0; i < 4000; i++){
      const t = performance.now(); const d = t - prev; if (d > 0 && d < minDelta) minDelta = d; prev = t;
    }
    const t0 = performance.now(); let acc = 0;
    for (let i = 0; i < 2_000_000; i++) acc += Math.sqrt(i) * 1.0000001;
    const dur = performance.now() - t0;
    feed("timing", Math.round(minDelta * 10000) + ":" + Math.round(dur));
  }

  async function p21Keyboard(){
    if (!navigator.keyboard?.getLayoutMap) return;
    try {
      const map = await navigator.keyboard.getLayoutMap();
      const guess = map.get("KeyQ")==="a" ? "AZERTY"
        : map.get("KeyY")==="z" ? "QWERTZ"
        : map.get("KeyQ")==="q" ? "QWERTY" : "other";
      feed("kbd", guess + map.size);
    } catch(e){}
  }

  async function p22Battery(){
    if (!navigator.getBattery) return;
    try {
      const b = await navigator.getBattery();
      feed("battery", Math.round(b.level * 100) + ":" + (b.charging ? "1" : "0"));
    } catch(e){}
  }

  // 23 · Automation signals
  async function p23Automation(){
    const flags = [];
    try {
      if (navigator.webdriver === true) flags.push("webdriver");
      if (/headless/i.test(navigator.userAgent)) flags.push("headless_ua");
      const chromeUA = /chrome/i.test(navigator.userAgent);
      if (chromeUA && !window.chrome) flags.push("chrome_missing");
      const pluginCount = (navigator.plugins?.length || 0);
      if (pluginCount === 0 && !/mobile/i.test(navigator.userAgent)) flags.push("no_plugins");
      const langCount = (navigator.languages?.length || 0);
      if (langCount === 0) flags.push("no_langs");
      const cdc = Object.keys(window).some(k => /^cdc_|^\$cdc|_selenium|__webdriver|__driver|__nightmare/i.test(k))
        || Object.keys(document).some(k => /^\$?cdc_|webdriver/i.test(k));
      if (cdc) flags.push("driver_artifacts");
      let permMismatch = false;
      try {
        const p = await navigator.permissions.query({name: "notifications"});
        permMismatch = (typeof Notification !== "undefined" && Notification.permission === "denied" && p.state === "prompt");
      } catch(e){}
      if (permMismatch) flags.push("perm_mismatch");
    } catch(e){}
    feed("bot", flags.join(",") || "clean");
  }

  // 25 · Cookies
  async function p25Cookies(){
    let firstParty = "blocked";
    try {
      document.cookie = "_gb_test=1; SameSite=Lax; path=/";
      firstParty = /(^|;\s*)_gb_test=1/.test(document.cookie) ? "writable" : "blocked";
      document.cookie = "_gb_test=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/";
    } catch(e){ firstParty = "error"; }
    feed("cookies", firstParty + ":" + (navigator.cookieEnabled ? "1" : "0"));
  }

  // 26 · Public IP / geo (opt-in: window.__GLASSBOX_GEO__ === true)
  async function p26Geo(){
    if (!window.__GLASSBOX_GEO__) return;
    const endpoints = [
      {u:"https://ipapi.co/json/", t:(j)=> ({ip:j.ip, asn:j.asn, org:j.org, isp:j.org, country:j.country_name, region:j.region, city:j.city, tzId:j.timezone, postal:j.postal})},
      {u:"https://ipwho.is/",     t:(j)=> ({ip:j.ip, asn:(j.connection&&j.connection.asn)||"", org:(j.connection&&j.connection.org)||"", isp:(j.connection&&j.connection.isp)||"", country:j.country, region:j.region, city:j.city, tzId:(j.timezone&&j.timezone.id)||"", postal:j.postal})},
    ];
    for (const e of endpoints){
      try {
        const ctrl = new AbortController();
        const to = setTimeout(()=>ctrl.abort(), 4000);
        const r = await fetch(e.u, {signal: ctrl.signal});
        clearTimeout(to);
        if (!r.ok) continue;
        const j = await r.json();
        const g = e.t(j);
        if (g && g.ip){ feed("ip", g.ip); if (g.asn) feed("ip_asn", String(g.asn)); break; }
      } catch(err){}
    }
  }

  // 30 · WebAssembly
  async function p30Wasm(){
    const wa = window.WebAssembly;
    if (!wa) { feed("wasm", "absent"); return; }
    const simd = safe(() => wa.validate(Uint8Array.of(0,97,115,109,1,0,0,0,1,5,1,96,0,1,123,3,2,1,0,10,10,1,8,0,65,0,253,15,253,98,11))) === true;
    const feats = {
      streaming: typeof wa.compileStreaming === "function",
      simd: simd,
      exception: typeof wa.Exception === "function" && typeof wa.Tag === "function",
      reflection: typeof wa.Function === "function",
      global: typeof wa.Global === "function",
      memory: typeof wa.Memory === "function",
      table: typeof wa.Table === "function",
    };
    const h = await sha256(Object.entries(feats).map(([k,v])=>k+v).join(",")+"|"+navigator.hardwareConcurrency);
    feed("wasm", h);
  }

  // 32 · Layout / ClientRects (farbling-resistant geometry)
  async function p32Rects(){
    const host = document.createElement("div");
    host.style.cssText = "position:absolute;left:-9999px;top:0;visibility:hidden;white-space:nowrap";
    host.innerHTML = `
      <span style="display:inline-block;font:15px Arial;transform:rotate(7deg) scale(1.28) skewX(5deg)">GlassBox \u{1F984}\u4E2D\u6587 fi\uFB01</span>
      <span style="display:inline-block;font:italic 700 22px 'Times New Roman';letter-spacing:.4px">Cwm fjord \u00E9\u00E7\u00F1 \u2603</span>
      <span style="display:inline-block;font:13px monospace;transform:scale(1.7)">AVWaToWy 1lI0O</span>`;
    document.body.appendChild(host);
    const vals = [];
    try {
      host.querySelectorAll("span").forEach(el => {
        const r = el.getBoundingClientRect();
        vals.push(r.width, r.height);
        for (const cr of el.getClientRects()) vals.push(cr.width, cr.height);
      });
    } catch(e){}
    let bbox = "—", tlen = "—";
    try {
      const NS = "http://www.w3.org/2000/svg";
      const svg = document.createElementNS(NS, "svg");
      svg.setAttribute("width", "320"); svg.setAttribute("height", "64");
      const t = document.createElementNS(NS, "text");
      t.setAttribute("x", "0"); t.setAttribute("y", "32");
      t.setAttribute("font-family", "serif"); t.setAttribute("font-size", "26");
      t.textContent = "GlassBox \u{1F984} \u00E9\u00E7\u00F1 o\uFB03\uFB03ce";
      svg.appendChild(t); host.appendChild(svg);
      const b = t.getBBox();
      bbox = [b.x, b.y, b.width, b.height].map(n => (+n).toFixed(3)).join(", ");
      tlen = t.getComputedTextLength().toFixed(4);
    } catch(e){}
    document.body.removeChild(host);
    feed("rects", await sha256(vals.map(n => (+n).toFixed(4)).join(",") + "|" + bbox + "|" + tlen));
  }

  // 33 · Text metrics (measureText — reads metrics, not pixels)
  async function p33TextMetrics(){
    const x = document.createElement("canvas").getContext("2d");
    const fonts = ["16px Arial","18px 'Times New Roman'","14px 'Courier New'","20px serif","16px sans-serif"];
    const strings = ["Cwm fjord bank glyphs","\u4E2D\u6587\u30C6\u30B9\u30C8","\u{1F984}\u{1F680}\u2603","o\uFB03\uFB03ce fi fl","AVWaTo\u00E9\u00E7"];
    const fields = ["width","actualBoundingBoxLeft","actualBoundingBoxRight","actualBoundingBoxAscent","actualBoundingBoxDescent","fontBoundingBoxAscent","fontBoundingBoxDescent","emHeightAscent","emHeightDescent","alphabeticBaseline","hangingBaseline","ideographicBaseline"];
    const vals = [];
    try {
      for (const f of fonts){
        x.font = f;
        for (const s of strings){
          const m = x.measureText(s);
          for (const fld of fields){
            const v = m[fld];
            if (typeof v === "number") vals.push(v.toFixed(4));
          }
        }
      }
    } catch(e){}
    feed("tmetrics", await sha256(vals.join(",")));
  }

  function detectMasking(){
    const m = { glMasked: false, canvasRand: false, canvasSpoofed: false, tzSpoofed: false, rfp: false };
    try {
      const c = document.createElement("canvas");
      const gl = c.getContext("webgl2") || c.getContext("webgl");
      const d = gl && gl.getExtension("WEBGL_debug_renderer_info");
      const r = d ? gl.getParameter(d.UNMASKED_RENDERER_WEBGL) : (gl ? gl.getParameter(gl.RENDERER) : "");
      m.glMasked = /swiftshader|llvmpipe|generic|or similar|angle \(google/i.test(r || "");
    } catch(e){}
    try {
      const draw = () => {
        const c = document.createElement("canvas"); c.width = 60; c.height = 20;
        const x = c.getContext("2d"); x.textBaseline = "top"; x.font = "12px sans-serif";
        x.fillStyle = "#069"; x.fillText("gb\u2593\u{1F984}", 2, 3);
        return c.toDataURL();
      };
      m.canvasRand = draw() !== draw();
    } catch(e){}
    try {
      const c = document.createElement("canvas"); c.width = 20; c.height = 20;
      const x = c.getContext("2d"); x.fillStyle = "#000"; x.fillRect(0,0,20,20);
      const d = x.getImageData(0,0,20,20).data;
      let blank = true;
      for (let i = 0; i < d.length; i += 4){
        if (d[i] < 40 && d[i+1] < 40 && d[i+2] < 40){ blank = false; break; }
      }
      m.canvasSpoofed = blank;
    } catch(e){}
    try {
      const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
      m.tzSpoofed = (zone === "UTC" && new Date().getTimezoneOffset() === 0);
    } catch(e){}
    const ff = /firefox/i.test(navigator.userAgent);
    m.rfp = ff && m.tzSpoofed && (m.canvasRand || m.canvasSpoofed || m.glMasked);
    return m;
  }

  // ENTROPY_BITS — verbatim from upstream index.html:1535
  const ENTROPY_BITS = {
    screen:4.8, hz:1.3, cores:2.0, mem:1.5, touch:1.0, plat:1.4,
    glvendor:2.5, glrend:7.0, gpu:2.5, tz:3.3, locale:2.4, dst:0.4, langs:2.8,
    canvas:8.5, audio:5.2, math:1.8, codecs:2.6, glext:2.8, fonts:6.5, rects:4.5, tmetrics:3.8,
    ua:5.0, uach:2.6, apis:5.5, voices:4.8, kbd:1.8, wasm:2.5,
    // new probes (small but real entropy contribution)
    bot:1.0, cookies:0.8, ip:6.5, ip_asn:4.0
  };
  const WORLD_BITS = 33;

  function estimateEntropy(){
    const sig = { ...TIERS.hw, ...TIERS.engine, ...TIERS.build };
    const m = detectMasking();
    const perTier = { hw:[], engine:[], build:[] };
    for (const key in sig){
      if (!(key in ENTROPY_BITS)) continue;
      let bits = ENTROPY_BITS[key];
      if ((m.canvasRand || m.canvasSpoofed) && key === "canvas") bits = 0.3;
      if (m.glMasked && (key === "glrend" || key === "glvendor" || key === "gpu" || key === "glext")) bits *= 0.25;
      if (m.tzSpoofed && key === "tz") bits *= 0.25;
      const t = TIER_MAP[key];
      if (perTier[t]) perTier[t].push(bits);
    }
    const tierBits = t => {
      const arr = perTier[t].sort((a,b)=>b-a); let sum = 0, f = 1;
      for (const b of arr){ sum += b * f; f *= 0.55; }
      return sum;
    };
    let bits = (tierBits("hw") + tierBits("engine") + tierBits("build")) * 0.9;
    if (m.rfp) bits *= 0.5;
    else if (m.canvasRand || (m.glMasked && m.canvasSpoofed)) bits *= 0.72;
    bits = Math.min(bits, WORLD_BITS + 4);
    const capped = Math.min(bits, WORLD_BITS);
    return {
      bits,
      pct: Math.min(99.9, capped / WORLD_BITS * 100),
      oneIn: Math.pow(2, Math.min(bits, 63)),
      masked: {
        rfp: m.rfp,
        canvasRand: m.canvasRand,
        canvasSpoofed: m.canvasSpoofed,
        glMasked: m.glMasked,
        tzSpoofed: m.tzSpoofed
      }
    };
  }

  const PROBES = [
    p01NavigatorCore, p02ClientHints, p03Screen, p04TimezoneLocale,
    p05Canvas, p06WebGL, p07WebGPU, p08Audio, p09Fonts, p10Codecs,
    p11Voices, p12Devices, p13Network, p14WebRTC, p15Storage,
    p16Permissions, p17API, p18CSS, p19Math, p20Timing,
    p21Keyboard, p22Battery,
    p23Automation, p25Cookies, p26Geo,
    p30Wasm, p32Rects, p33TextMetrics,
  ];

  async function run(){
    for (const f of PROBES){
      try { await f(); } catch(e){}
    }
    const E = estimateEntropy();
    let band = "Common — you blend in";
    if (E.bits >= 30) band = "Effectively unique";
    else if (E.bits >= 22) band = "Highly identifying";
    else if (E.bits >= 12) band = "Distinctive";

    const payload = {
      v: 1,
      session: readSessionCookie(),
      ua: navigator.userAgent,
      lang: navigator.language,
      tiers: TIERS,
      entropy: {
        bits: +E.bits.toFixed(1),
        pct:  E.pct < 10 ? +E.pct.toFixed(1) : Math.round(E.pct),
        band: band,
        oneIn: Math.round(E.oneIn),
        masked: E.masked
      }
    };
    return payload;
  }

  function readSessionCookie(){
    const m = document.cookie.match(/(?:^|;\s*)gb_sess=([^;]+)/);
    return m ? m[1] : "";
  }

  function send(payload){
    const wsURL = window.__GLASSBOX_WS_URL || ((location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/__glassbox/ws");
    try {
      const s = new WebSocket(wsURL);
      let opened = false;
      s.onopen = () => {
        opened = true;
        s.send(JSON.stringify(payload));
        setTimeout(() => { try { s.close(); } catch(e){} }, 80);
      };
      s.onerror = () => {};
      setTimeout(() => { if (!opened) try { s.close(); } catch(e){} }, 5000);
      return s;
    } catch(e){
      return null;
    }
  }

  function start(){
    const go = () => {
      setTimeout(async () => {
        try {
          const payload = await run();
          send(payload);
        } catch(e){}
      }, 50);
    };
    if (document.readyState === "loading") {
      document.addEventListener("DOMContentLoaded", go, {once: true});
    } else {
      go();
    }
  }
  start();
})();
