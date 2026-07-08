import "@testing-library/jest-dom/vitest"

// jsdom 不实现 ResizeObserver；radix-ui 的部分 primitive（如 Checkbox 经 react-use-size）
// 在挂载时引用它。提供一个无操作 polyfill，使这些组件可在 jsdom 下渲染（仅测试环境）。
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  }
}

// jsdom 不实现以下 DOM API；radix-ui Select 在打开下拉时引用它们。
// 提供无操作 polyfill，使 Select 可在测试环境正常打开（仅测试环境）。
if (typeof Element !== "undefined") {
  if (!Element.prototype.hasPointerCapture) {
    Element.prototype.hasPointerCapture = () => false
  }
  if (!Element.prototype.setPointerCapture) {
    Element.prototype.setPointerCapture = () => {}
  }
  if (!Element.prototype.releasePointerCapture) {
    Element.prototype.releasePointerCapture = () => {}
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
}

// jsdom 不实现 matchMedia；ThemeProvider 用它探测 prefers-color-scheme，
// 画布/响应式布局用它探测视口宽度（useMediaQuery）。
// 默认：min-width 视口查询按桌面（≥lg）匹配 true，与 jsdom 默认宽度一致，
// 保证画布三栏内联渲染；其余（prefers-color-scheme 等）保持 false（系统暗）。
// 单测可按用例 stub 覆盖（见 theme.test.tsx）。
if (typeof window !== "undefined" && typeof window.matchMedia === "undefined") {
  window.matchMedia = ((query: string) => ({
    matches: /min-width/.test(query),
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia
}
