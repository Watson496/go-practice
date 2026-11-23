import { beforeEach } from "vitest";

class TestResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

Object.defineProperty(window, "ResizeObserver", {
  configurable: true,
  value: TestResizeObserver,
});
Object.defineProperty(window, "visualViewport", {
  configurable: true,
  value: { addEventListener() {}, removeEventListener() {} },
});
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({
    matches: false,
    media: "",
    addEventListener() {},
    removeEventListener() {},
  }),
});
Object.defineProperties(window.HTMLElement.prototype, {
  attachEvent: { configurable: true, value() {} },
  detachEvent: { configurable: true, value() {} },
  scrollIntoView: { configurable: true, value() {} },
});
Object.defineProperty(document, "fonts", {
  configurable: true,
  value: { addEventListener() {}, removeEventListener() {} },
});
Object.defineProperties(globalThis, {
  ResizeObserver: { configurable: true, value: TestResizeObserver },
  IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
});

beforeEach(() => {
  document.body.innerHTML = '<div id="root"></div>';
  localStorage.clear();
});
