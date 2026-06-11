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
