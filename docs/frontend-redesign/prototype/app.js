/* ══════════════════════════════════════════════════════════════════
   NovaVeil 前端美化原型 v2 · app.js
   IIFE · 零依赖 · 主题感知 SVG · 完整 a11y
   ══════════════════════════════════════════════════════════════════ */
(function () {
  'use strict';

  const $  = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
  const NS = 'http://www.w3.org/2000/svg';
  const el = (tag, attrs = {}) => {
    const e = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
    return e;
  };

  let flowTimer = null;
  let nodeTimer = null;
  let resizeRAF = null;

  /* ──────────────────────────────────────────────
     1. 视图切换 + 焦点管理
     ────────────────────────────────────────────── */
  function switchView(name) {
    $$('.view').forEach(v => v.classList.remove('is-active'));
    const target = $('#view-' + name);
    if (!target) return;
    target.classList.add('is-active');

    // 更新 tablist aria-selected
    $$('#viewTabs .seg__btn').forEach(btn => {
      const active = btn.dataset.view === name;
      btn.classList.toggle('is-active', active);
      btn.setAttribute('aria-selected', String(active));
    });

    // 焦点移入新视图（键盘可达）
    target.focus({ preventScroll: true });

    // 流向动画 timer 清理
    if (flowTimer) { clearInterval(flowTimer); flowTimer = null; }
    if (nodeTimer) { clearInterval(nodeTimer); nodeTimer = null; }

    // 按需绘制
    if (name === 'landing') {
      requestAnimationFrame(() => { drawFlow(); startFlowAnimation(); activateFlowNode(); });
    } else if (name === 'dashboard') {
      requestAnimationFrame(() => { drawTrend(); drawDonut(); });
    }

    // 关闭移动端侧栏
    closeMobileSidebar();
  }

  /* ──────────────────────────────────────────────
     2. 装饰档位
     ────────────────────────────────────────────── */
  function setFlair(flair) {
    document.documentElement.dataset.flair = flair;
    $$('#flairTabs .seg__btn').forEach(btn => {
      const active = btn.dataset.flair === flair;
      btn.classList.toggle('is-active', active);
      btn.setAttribute('aria-selected', String(active));
    });
  }

  /* ──────────────────────────────────────────────
     3. 主题切换
     ────────────────────────────────────────────── */
  function toggleTheme() {
    const root = document.documentElement;
    const isDark = root.dataset.theme === 'dark';
    root.dataset.theme = isDark ? 'light' : 'dark';
    const btn = $('#themeToggle');
    btn.setAttribute('aria-label', isDark ? '切换到暗色主题' : '切换到亮色主题');
    btn.setAttribute('aria-pressed', String(!isDark));

    // 强制重画图表（清除 drawn 标记 + 清空内容）
    if ($('#view-landing').classList.contains('is-active')) drawFlow();
    if ($('#view-dashboard').classList.contains('is-active')) {
      const trend = $('#trendChart');
      const donut = $('#donutChart');
      const legend = $('#donutLegend');
      if (trend) { trend.removeAttribute('data-drawn'); while (trend.firstChild) trend.removeChild(trend.firstChild); }
      if (donut) { donut.removeAttribute('data-drawn'); while (donut.firstChild) donut.removeChild(donut.firstChild); }
      if (legend) legend.innerHTML = '';
      drawTrend();
      drawDonut();
    }
  }

  /* ──────────────────────────────────────────────
     4. 流向动画
     ────────────────────────────────────────────── */
  function drawFlow() {
    const svg = $('#flowSvg');
    const container = $('#flowDiagram');
    if (!svg || !container) return;

    const rect = container.getBoundingClientRect();
    if (rect.width === 0) return;
    svg.setAttribute('viewBox', `0 0 ${rect.width} ${rect.height}`);

    // 清空
    while (svg.firstChild) svg.removeChild(svg.firstChild);

    // defs — 渐变 + 滤镜
    const defs = el('defs');
    const grad = el('linearGradient', { id: 'flowGrad', x1: '0', y1: '0', x2: '1', y2: '0' });
    grad.appendChild(el('stop', { offset: '0', 'stop-color': '#007AFF', 'stop-opacity': '0.5' }));
    grad.appendChild(el('stop', { offset: '0.5', 'stop-color': '#5856D6', 'stop-opacity': '0.8' }));
    grad.appendChild(el('stop', { offset: '1', 'stop-color': '#007AFF', 'stop-opacity': '0.5' }));
    defs.appendChild(grad);
    svg.appendChild(defs);

    const hub = $('.flow__hub', container);
    if (!hub) return;
    const hubRect = hub.getBoundingClientRect();
    const cx = hubRect.left + hubRect.width / 2 - rect.left;
    const cy = hubRect.top + hubRect.height / 2 - rect.top;

    // 连线
    const leftNodes  = $$('.flow__col--left .flow__node', container);
    const rightNodes = $$('.flow__col--right .flow__node', container);

    [...leftNodes, ...rightNodes].forEach(node => {
      const nr = node.getBoundingClientRect();
      const nx = nr.left + nr.width / 2 - rect.left;
      const ny = nr.top + nr.height / 2 - rect.top;
      const isLeft = node.closest('.flow__col--left');
      const ctrlX = isLeft ? cx - 40 : cx + 40;

      const path = el('path', {
        d: `M ${nx} ${ny} C ${ctrlX} ${ny}, ${ctrlX} ${cy}, ${cx} ${cy}`,
        stroke: 'url(#flowGrad)',
        'stroke-width': '1.5',
        fill: 'none',
        'stroke-dasharray': '4 6',
        opacity: '0.6',
      });
      svg.appendChild(path);
    });
  }

  function startFlowAnimation() {
    if (flowTimer) clearInterval(flowTimer);
    let offset = 0;
    flowTimer = setInterval(() => {
      offset = (offset - 1) % 1000;
      $$('#flowSvg path').forEach(p => p.setAttribute('stroke-dashoffset', offset));
    }, 50);
  }

  function activateFlowNode() {
    if (nodeTimer) clearInterval(nodeTimer);
    const leftNodes  = $$('.flow__col--left .flow__node');
    const rightNodes = $$('.flow__col--right .flow__node');
    let i = 0;
    nodeTimer = setInterval(() => {
      leftNodes.forEach(n => n.classList.remove('is-active'));
      rightNodes.forEach(n => n.classList.remove('is-active'));
      leftNodes[i % leftNodes.length]?.classList.add('is-active');
      rightNodes[(i + 2) % rightNodes.length]?.classList.add('is-active');
      i++;
    }, 1500);
  }

  /* ──────────────────────────────────────────────
     5. 趋势图
     ────────────────────────────────────────────── */
  function drawTrend() {
    const svg = $('#trendChart');
    if (!svg || svg.dataset.drawn === 'true') return;

    const W = 720, H = 220, pad = { l: 40, r: 16, t: 16, b: 28 };
    const cw = W - pad.l - pad.r, ch = H - pad.t - pad.b;

    // 读取主题色
    const style = getComputedStyle(document.documentElement);
    const textMuted = style.getPropertyValue('--text-muted').trim() || '#A0A0B0';
    const textSubtle = style.getPropertyValue('--text-subtle').trim() || '#84849A';
    const border = style.getPropertyValue('--border').trim() || 'rgba(255,255,255,0.08)';

    // 模拟数据
    const days = 30;
    const input = [], output = [];
    for (let i = 0; i < days; i++) {
      input.push(0.4 + Math.sin(i * 0.3) * 0.15 + Math.random() * 0.2 + i * 0.012);
      output.push(0.25 + Math.cos(i * 0.25) * 0.1 + Math.random() * 0.15 + i * 0.008);
    }
    const maxVal = Math.max(...input, ...output) * 1.15;

    const x = i => pad.l + (i / (days - 1)) * cw;
    const y = v => pad.t + ch - (v / maxVal) * ch;

    // defs
    const defs = el('defs');
    ['g1', 'g2'].forEach((id, idx) => {
      const c = idx === 0 ? '#007AFF' : '#5856D6';
      const g = el('linearGradient', { id, x1: '0', y1: '0', x2: '0', y2: '1' });
      g.appendChild(el('stop', { offset: '0', 'stop-color': c, 'stop-opacity': '0.22' }));
      g.appendChild(el('stop', { offset: '1', 'stop-color': c, 'stop-opacity': '0' }));
      defs.appendChild(g);
    });
    svg.appendChild(defs);

    // 网格线
    for (let i = 0; i <= 4; i++) {
      const gy = pad.t + (ch / 4) * i;
      svg.appendChild(el('line', { x1: pad.l, y1: gy, x2: W - pad.r, y2: gy, stroke: border, 'stroke-width': '1' }));
      const val = Math.round(maxVal * (1 - i / 4) * 100);
      const t = el('text', { x: pad.l - 8, y: gy + 4, 'text-anchor': 'end', fill: textSubtle, 'font-size': '10' });
      t.textContent = val + 'k';
      svg.appendChild(t);
    }

    // X 轴标签
    ['1日', '8日', '15日', '22日', '30日'].forEach((label, i) => {
      const tx = pad.l + (i / 4) * cw;
      const t = el('text', { x: tx, y: H - 8, 'text-anchor': 'middle', fill: textSubtle, 'font-size': '10' });
      t.textContent = label;
      svg.appendChild(t);
    });

    // 折线 + 面积
    [{ data: input, color: '#007AFF', grad: 'g1' }, { data: output, color: '#5856D6', grad: 'g2' }].forEach(({ data, color, grad }) => {
      let line = `M ${x(0)} ${y(data[0])}`;
      let area = `M ${x(0)} ${y(data[0])}`;
      for (let i = 1; i < days; i++) {
        const px = x(i), py = y(data[i]);
        const prevX = x(i - 1), prevY = y(data[i - 1]);
        const cpx1 = prevX + (px - prevX) / 2, cpy1 = prevY;
        const cpx2 = prevX + (px - prevX) / 2, cpy2 = py;
        line += ` C ${cpx1} ${cpy1}, ${cpx2} ${cpy2}, ${px} ${py}`;
        area += ` C ${cpx1} ${cpy1}, ${cpx2} ${cpy2}, ${px} ${py}`;
      }
      area += ` L ${x(days - 1)} ${pad.t + ch} L ${x(0)} ${pad.t + ch} Z`;

      svg.appendChild(el('path', { d: area, fill: `url(#${grad})` }));
      svg.appendChild(el('path', { d: line, stroke: color, 'stroke-width': '2', fill: 'none', 'stroke-linecap': 'round', 'stroke-linejoin': 'round' }));
    });

    svg.dataset.drawn = 'true';
  }

  /* ──────────────────────────────────────────────
     6. 甜甜圈图
     ────────────────────────────────────────────── */
  function drawDonut() {
    const svg = $('#donutChart');
    const legend = $('#donutLegend');
    if (!svg || !legend || svg.dataset.drawn === 'true') return;

    const data = [
      { label: 'claude-3.5-sonnet', val: 38, color: '#007AFF' },
      { label: 'gpt-4o',           val: 25, color: '#5856D6' },
      { label: 'gemini-2.0-flash',  val: 18, color: '#FF9F0A' },
      { label: 'deepseek-v3',       val: 12, color: '#34C759' },
      { label: '其他',              val: 7,  color: '#8E8E93' },
    ];

    const cx = 80, cy = 80, r = 56, sw = 14;
    let acc = 0;
    const gap = 0.02;

    data.forEach(d => {
      const start = acc * 2 * Math.PI - Math.PI / 2;
      acc += d.val / 100;
      const end = acc * 2 * Math.PI - Math.PI / 2 - gap;

      const x1 = cx + r * Math.cos(start), y1 = cy + r * Math.sin(start);
      const x2 = cx + r * Math.cos(end),   y2 = cy + r * Math.sin(end);
      const large = (end - start) > Math.PI ? 1 : 0;

      svg.appendChild(el('path', {
        d: `M ${x1} ${y1} A ${r} ${r} 0 ${large} 1 ${x2} ${y2}`,
        stroke: d.color, 'stroke-width': String(sw), fill: 'none',
        'stroke-linecap': 'round',
      }));

      // 图例
      const item = document.createElement('div');
      item.className = 'donut-legend__item';
      item.innerHTML =
        `<span class="donut-legend__dot" style="background:${d.color}"></span>` +
        `<span class="donut-legend__label">${d.label}</span>` +
        `<span class="donut-legend__val">${d.val}%</span>`;
      legend.appendChild(item);
    });

    // 中心文字 — 主题感知
    const style = getComputedStyle(document.documentElement);
    const textCol = style.getPropertyValue('--text').trim() || '#1D1D1F';
    const subtleCol = style.getPropertyValue('--text-subtle').trim() || '#86868B';
    const t1 = el('text', { x: cx, y: cy - 2, 'text-anchor': 'middle', fill: textCol, 'font-size': '20', 'font-weight': '700' });
    t1.textContent = '2.1M';
    svg.appendChild(t1);
    const t2 = el('text', { x: cx, y: cy + 14, 'text-anchor': 'middle', fill: subtleCol, 'font-size': '10' });
    t2.textContent = 'tokens';
    svg.appendChild(t2);

    svg.dataset.drawn = 'true';
  }

  /* ──────────────────────────────────────────────
     7. 侧栏
     ────────────────────────────────────────────── */
  function toggleCollapse() {
    const sb = $('#sidebar');
    const collapsed = sb.classList.toggle('is-collapsed');
    $('#collapseBtn').setAttribute('aria-expanded', String(!collapsed));

    // 折叠态导航项加 title
    $$('.sb__item').forEach(item => {
      if (collapsed) item.setAttribute('title', item.dataset.label || '');
      else item.removeAttribute('title');
    });
  }

  function initSidebarSearch() {
    const input = $('#sbSearch');
    if (!input) return;
    input.addEventListener('input', () => {
      const q = input.value.trim().toLowerCase();
      $$('.sb__item').forEach(item => {
        const label = (item.dataset.label || '').toLowerCase();
        item.classList.toggle('is-hidden', q && !label.includes(q));
      });
    });
  }

  function openMobileSidebar() {
    $('#sidebar').classList.add('is-mobile-open');
    $('#sbOverlay').classList.add('is-visible');
    $('#sbOverlay').setAttribute('aria-hidden', 'false');
  }
  function closeMobileSidebar() {
    $('#sidebar').classList.remove('is-mobile-open');
    $('#sbOverlay').classList.remove('is-visible');
    $('#sbOverlay').setAttribute('aria-hidden', 'true');
  }

  /* ──────────────────────────────────────────────
     8. Toast
     ────────────────────────────────────────────── */
  let toastTimer = null;
  function showToast(msg) {
    const t = $('#toast');
    t.textContent = msg;
    t.classList.add('is-visible');
    if (toastTimer) clearTimeout(toastTimer);
    toastTimer = setTimeout(() => t.classList.remove('is-visible'), 2500);
  }

  /* ──────────────────────────────────────────────
     9. 复制按钮
     ────────────────────────────────────────────── */
  function initCopy() {
    const btn = $('#copyBtn');
    if (!btn) return;
    btn.addEventListener('click', async () => {
      const code = $('.codeblock__code code')?.textContent || '';
      try {
        if (navigator.clipboard?.writeText) {
          await navigator.clipboard.writeText(code);
        } else {
          // fallback
          const ta = document.createElement('textarea');
          ta.value = code; ta.style.position = 'fixed'; ta.style.opacity = '0';
          document.body.appendChild(ta); ta.select();
          document.execCommand('copy'); document.body.removeChild(ta);
        }
        showToast('已复制到剪贴板');
      } catch {
        showToast('复制失败，请手动复制');
      }
    });
  }

  /* ──────────────────────────────────────────────
     10. 登录表单
     ────────────────────────────────────────────── */
  function initLogin() {
    const form = $('#loginForm');
    if (!form) return;
    form.addEventListener('submit', e => {
      e.preventDefault();
      const user = $('#loginUser').value.trim();
      const pass = $('#loginPass').value;
      if (!user || !pass) { showToast('请填写用户名和密码'); return; }
      showToast('登录成功（原型演示）');
      setTimeout(() => switchView('dashboard'), 800);
    });
  }

  /* ──────────────────────────────────────────────
     11. 范围标签
     ────────────────────────────────────────────── */
  function initRangeTabs() {
    $$('.range-tabs__btn').forEach(btn => {
      btn.addEventListener('click', () => {
        const group = btn.closest('.range-tabs');
        $$('.range-tabs__btn', group).forEach(b => {
          b.classList.remove('is-active');
          b.setAttribute('aria-selected', 'false');
        });
        btn.classList.add('is-active');
        btn.setAttribute('aria-selected', 'true');
      });
    });
  }

  /* ──────────────────────────────────────────────
     12. 导航跳转
     ────────────────────────────────────────────── */
  function initNavJumps() {
    $$('[data-goto]').forEach(btn => {
      btn.addEventListener('click', () => switchView(btn.dataset.goto));
    });
  }

  /* ──────────────────────────────────────────────
     13. Resize
     ────────────────────────────────────────────── */
  function onResize() {
    if (resizeRAF) cancelAnimationFrame(resizeRAF);
    resizeRAF = requestAnimationFrame(() => {
      if ($('#view-landing').classList.contains('is-active')) drawFlow();
      // trend/donut 用 preserveAspectRatio 自适应，无需重画
    });
  }

  /* ──────────────────────────────────────────────
     14. 初始化
     ────────────────────────────────────────────── */
  function init() {
    // 视图切换
    $$('#viewTabs .seg__btn').forEach(btn => {
      btn.addEventListener('click', () => switchView(btn.dataset.view));
    });

    // 装饰档位
    $$('#flairTabs .seg__btn').forEach(btn => {
      btn.addEventListener('click', () => setFlair(btn.dataset.flair));
    });

    // 主题
    $('#themeToggle').addEventListener('click', toggleTheme);

    // 侧栏
    $('#collapseBtn').addEventListener('click', toggleCollapse);
    $('#menuBtn').addEventListener('click', openMobileSidebar);
    $('#sbClose').addEventListener('click', closeMobileSidebar);
    $('#sbOverlay').addEventListener('click', closeMobileSidebar);
    initSidebarSearch();

    // 其他
    initCopy();
    initLogin();
    initRangeTabs();
    initNavJumps();

    // 初始绘制
    drawFlow();
    startFlowAnimation();
    activateFlowNode();

    // resize
    window.addEventListener('resize', onResize);

    // 字体加载后重画 flow（修正尺寸漂移）
    if (document.fonts?.ready) {
      document.fonts.ready.then(() => {
        if ($('#view-landing').classList.contains('is-active')) drawFlow();
      });
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
