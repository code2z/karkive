(() => {
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  const year = document.getElementById("year");
  if (year) year.textContent = String(new Date().getFullYear());

  /* Tabs (WAI-ARIA tabs pattern) */
  document.querySelectorAll("[data-tabs]").forEach((group) => {
    const tabs = [...group.querySelectorAll('[role="tab"]')];
    const select = (tab, focus) => {
      tabs.forEach((t) => {
        const on = t === tab;
        t.setAttribute("aria-selected", String(on));
        t.tabIndex = on ? 0 : -1;
        const panel = document.getElementById(t.getAttribute("aria-controls"));
        if (panel) panel.hidden = !on;
      });
      if (focus) tab.focus();
    };
    tabs.forEach((tab, i) => {
      tab.addEventListener("click", () => select(tab, false));
      tab.addEventListener("keydown", (e) => {
        const next = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: tabs.length - 1 }[e.key];
        if (next === undefined) return;
        e.preventDefault();
        select(tabs[(next + tabs.length) % tabs.length], true);
      });
    });
  });

  /* Copy buttons: copy the visible panel of their group */
  document.querySelectorAll("[data-copy-active]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const group = btn.closest("[data-tabs]");
      const panel = group && [...group.querySelectorAll('[role="tabpanel"]')].find((p) => !p.hidden);
      if (!panel) return;
      try {
        await navigator.clipboard.writeText(panel.innerText.replace(/\n+$/, "") + "\n");
        btn.dataset.copied = "true";
        btn.textContent = "Copied";
      } catch {
        btn.textContent = "Copy failed";
      }
      window.setTimeout(() => {
        delete btn.dataset.copied;
        btn.textContent = "Copy";
      }, 1600);
    });
  });

  /* Hero terminal: play the pipeline stage by stage */
  const term = document.querySelector(".term");
  if (term) {
    const stages = [...term.querySelectorAll(".term-stages li")];
    const lines = [...term.querySelectorAll(".term-log .line")];
    let timers = [];

    // step k: stages < k done, stage k running, log lines with data-at < k shown.
    // step === stages.length: all done, every line shown (including the final data-at = length line).
    const show = (step) => {
      stages.forEach((s, i) => {
        s.classList.toggle("done", i < step);
        s.classList.toggle("running", i === step);
      });
      lines.forEach((l) => l.classList.toggle("pending", Number(l.dataset.at) >= step && step < stages.length));
    };

    const play = () => {
      timers.forEach(clearTimeout);
      timers = [];
      if (reducedMotion) {
        show(stages.length);
        return;
      }
      show(-1);
      for (let step = 0; step <= stages.length; step++) {
        timers.push(window.setTimeout(() => show(step), 300 + step * 900));
      }
    };

    term.querySelector("[data-replay]")?.addEventListener("click", play);

    // Start now if the terminal is on screen; otherwise when it scrolls in.
    const r = term.getBoundingClientRect();
    if (r.top < window.innerHeight && r.bottom > 0) {
      play();
    } else if ("IntersectionObserver" in window) {
      show(-1);
      const io = new IntersectionObserver(
        (entries) => {
          if (entries.some((e) => e.isIntersecting)) {
            play();
            io.disconnect();
          }
        },
        { threshold: 0.3 }
      );
      io.observe(term);
    } else {
      show(stages.length);
    }
  }

  /* Charts: sample data, drawn as SVG with a crosshair tooltip */
  const SVG = "http://www.w3.org/2000/svg";
  const el = (name, attrs, parent) => {
    const node = document.createElementNS(SVG, name);
    Object.entries(attrs || {}).forEach(([k, v]) => node.setAttribute(k, v));
    if (parent) parent.appendChild(node);
    return node;
  };

  // Deterministic sample series: 25 hourly points.
  const hours = Array.from({ length: 25 }, (_, i) => i);
  const wave = (i, a, b, c) => a + b * Math.sin(i / c) + ((i * 37) % 7) - 3;
  const data = {
    duration: {
      unit: "s",
      series: [
        { key: "backup", name: "Backup", values: hours.map((i) => Math.round(wave(i, 46, 14, 3.1))) },
        { key: "restore", name: "Restore", values: hours.map((i) => Math.round(wave(i + 5, 60, 9, 4.2))) },
      ],
    },
    age: {
      unit: "h",
      threshold: { value: 36, label: "36h stale" },
      series: [{ key: "backup", name: "Oldest backup", area: true, values: hours.map((i) => Math.max(1, +(((i * 1.3) % 24) + 2).toFixed(1))) }],
    },
  };

  const label = (h) => (h === 24 ? "now" : `${String(h).padStart(2, "0")}:00`);

  const drawChart = (host) => {
    const spec = data[host.dataset.chart];
    if (!spec) return;
    host.textContent = "";
    const w = host.clientWidth;
    const h = host.clientHeight;
    const m = { t: 8, r: 46, b: 20, l: 30 };
    const iw = w - m.l - m.r;
    const ih = h - m.t - m.b;
    const all = spec.series.flatMap((s) => s.values).concat(spec.threshold ? [spec.threshold.value] : []);
    const max = Math.ceil((Math.max(...all) * 1.1) / 10) * 10;
    const x = (i) => m.l + (i / (hours.length - 1)) * iw;
    const y = (v) => m.t + ih - (v / max) * ih;

    const svg = el("svg", { viewBox: `0 0 ${w} ${h}`, "aria-hidden": "true" }, host);
    const grid = el("g", { class: "grid" }, svg);
    const axis = el("g", { class: "axis" }, svg);
    [0, 0.5, 1].forEach((f) => {
      const v = Math.round(max * f);
      el("line", { x1: m.l, x2: m.l + iw, y1: y(v), y2: y(v) }, grid);
      el("text", { x: m.l - 6, y: y(v) + 3, "text-anchor": "end" }, axis).textContent = v;
    });
    [0, 12, 24].forEach((i) => {
      el("text", { x: x(i), y: h - 4, "text-anchor": i === 0 ? "start" : i === 24 ? "end" : "middle" }, axis).textContent = label(i);
    });

    if (spec.threshold) {
      el("line", { class: "threshold", x1: m.l, x2: m.l + iw, y1: y(spec.threshold.value), y2: y(spec.threshold.value) }, svg);
      el("text", { class: "threshold-label", x: m.l + iw + 6, y: y(spec.threshold.value) + 3 }, svg).textContent = spec.threshold.label;
    }

    const path = (vals) => vals.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join("");
    spec.series.forEach((s) => {
      if (s.area) el("path", { class: "area", d: `${path(s.values)}L${x(hours.length - 1)},${y(0)}L${x(0)},${y(0)}Z` }, svg);
      el("path", { class: `series ${s.key}`, d: path(s.values) }, svg);
    });

    // Direct end labels (<= 2 series), nudged apart if they collide.
    const ends = spec.series.map((s) => ({ s, y: y(s.values[s.values.length - 1]) }));
    if (ends.length === 2 && Math.abs(ends[0].y - ends[1].y) < 12) {
      const mid = (ends[0].y + ends[1].y) / 2;
      const up = ends[0].y <= ends[1].y ? 0 : 1;
      ends[up].y = mid - 6;
      ends[1 - up].y = mid + 6;
    }
    ends.forEach(({ s, y: ly }) => {
      el("text", { class: "end-label", x: m.l + iw + 6, y: ly + 3 }, svg).textContent = `${s.values[s.values.length - 1]}${spec.unit}`;
    });

    // Hover layer
    const cross = el("line", { class: "cross", y1: m.t, y2: m.t + ih, visibility: "hidden" }, svg);
    const dots = spec.series.map((s) => el("circle", { class: `dot ${s.key}`, r: 4, visibility: "hidden" }, svg));
    const hit = el("rect", { class: "hit", x: m.l, y: 0, width: iw, height: h }, svg);
    const tip = document.createElement("div");
    tip.className = "tip";
    tip.hidden = true;
    host.appendChild(tip);

    const move = (clientX) => {
      const r = svg.getBoundingClientRect();
      const i = Math.max(0, Math.min(hours.length - 1, Math.round(((clientX - r.left - m.l) / iw) * (hours.length - 1))));
      const cx = x(i);
      cross.setAttribute("x1", cx);
      cross.setAttribute("x2", cx);
      cross.setAttribute("visibility", "visible");
      spec.series.forEach((s, k) => {
        dots[k].setAttribute("cx", cx);
        dots[k].setAttribute("cy", y(s.values[i]));
        dots[k].setAttribute("visibility", "visible");
      });
      tip.innerHTML =
        `<b>${label(i)}</b>` +
        spec.series.map((s) => `<span class="row"><i class="sw sw-${s.key}"></i>${s.name}<span class="v">${s.values[i]}${spec.unit}</span></span>`).join("");
      tip.hidden = false;
      const tw = tip.offsetWidth;
      const left = Math.max(0, Math.min(w - tw, cx - tw / 2));
      tip.style.left = `${left}px`;
      tip.style.top = `${Math.max(0, m.t - tip.offsetHeight - 6)}px`;
    };
    const hide = () => {
      cross.setAttribute("visibility", "hidden");
      dots.forEach((d) => d.setAttribute("visibility", "hidden"));
      tip.hidden = true;
    };
    hit.addEventListener("pointermove", (e) => move(e.clientX));
    hit.addEventListener("pointerdown", (e) => move(e.clientX));
    hit.addEventListener("pointerleave", hide);
  };

  const charts = [...document.querySelectorAll("[data-chart]")];
  const drawAll = () => charts.forEach(drawChart);
  drawAll();
  let resizeTimer;
  window.addEventListener("resize", () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(drawAll, 120);
  });

  // Table view of the same data
  const tableHost = document.querySelector("[data-chart-table]");
  if (tableHost) {
    const cols = [
      ["Backup duration", data.duration.series[0].values, "s"],
      ["Restore duration", data.duration.series[1].values, "s"],
      ["Oldest backup age", data.age.series[0].values, "h"],
    ];
    tableHost.innerHTML =
      `<table><thead><tr><th>Time</th>${cols.map((c) => `<th>${c[0]}</th>`).join("")}</tr></thead><tbody>` +
      hours.map((i) => `<tr><td>${label(i)}</td>${cols.map((c) => `<td>${c[1][i]}${c[2]}</td>`).join("")}</tr>`).join("") +
      "</tbody></table>";
  }

  /* Active nav link */
  const navLinks = [...document.querySelectorAll('.nav nav a[href^="#"]')];
  const sections = navLinks.map((a) => document.querySelector(a.getAttribute("href"))).filter(Boolean);
  let active = null;
  const setActive = (id) => {
    if (id === active) return;
    active = id;
    navLinks.forEach((a) => {
      const on = a.getAttribute("href") === `#${id}`;
      a.classList.toggle("active", on);
      if (on) {
        a.setAttribute("aria-current", "location");
        const nav = a.parentElement;
        if (nav.scrollWidth > nav.clientWidth) {
          nav.scrollTo({ left: a.offsetLeft - nav.clientWidth / 2 + a.offsetWidth / 2, behavior: reducedMotion ? "auto" : "smooth" });
        }
      } else a.removeAttribute("aria-current");
    });
  };
  const onScroll = () => {
    const marker = window.scrollY + document.querySelector(".nav").offsetHeight + 120;
    let current = null;
    sections.forEach((s) => {
      if (s.offsetTop <= marker) current = s.id;
    });
    if (window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 4) current = sections[sections.length - 1]?.id ?? current;
    setActive(current);
  };
  let ticking = false;
  window.addEventListener(
    "scroll",
    () => {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(() => {
        onScroll();
        ticking = false;
      });
    },
    { passive: true }
  );
  onScroll();
})();
