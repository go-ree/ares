import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';
import '@testing-library/jest-dom/vitest';
import { resetApiAuth } from '@shared/config/api';

// Semi reads both of these during render; jsdom implements neither.
class ResizeObserverStub implements ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

Object.defineProperty(globalThis, 'ResizeObserver', {
  configurable: true,
  value: ResizeObserverStub,
});

// Semi's package entry imports lottie-web, which builds a 2D canvas context at
// module scope. jsdom implements no canvas, so without this stub any spec that
// imports a Semi component fails while loading.
const canvasContextStub = () => ({
  fillStyle: '',
  strokeStyle: '',
  lineWidth: 1,
  globalAlpha: 1,
  globalCompositeOperation: 'source-over',
  save() {},
  restore() {},
  scale() {},
  rotate() {},
  translate() {},
  transform() {},
  setTransform() {},
  resetTransform() {},
  clearRect() {},
  fillRect() {},
  strokeRect() {},
  beginPath() {},
  closePath() {},
  moveTo() {},
  lineTo() {},
  bezierCurveTo() {},
  quadraticCurveTo() {},
  arc() {},
  rect() {},
  fill() {},
  stroke() {},
  clip() {},
  drawImage() {},
  putImageData() {},
  fillText() {},
  strokeText() {},
  setLineDash() {},
  getLineDash: () => [],
  measureText: () => ({ width: 0 }),
  getImageData: () => ({ data: [] }),
  createLinearGradient: () => ({ addColorStop() {} }),
  createRadialGradient: () => ({ addColorStop() {} }),
  createPattern: () => ({}),
});

Object.defineProperty(HTMLCanvasElement.prototype, 'getContext', {
  configurable: true,
  value: () => canvasContextStub(),
});

// Semi measures text and popups with Range geometry, which jsdom does not
// implement. Without these stubs Nav/Dropdown/Tooltip throw during render.
const emptyRect = {
  x: 0,
  y: 0,
  width: 0,
  height: 0,
  top: 0,
  right: 0,
  bottom: 0,
  left: 0,
  toJSON: () => ({}),
};

Object.defineProperty(Range.prototype, 'getBoundingClientRect', {
  configurable: true,
  value: () => emptyRect,
});

Object.defineProperty(Range.prototype, 'getClientRects', {
  configurable: true,
  value: () => ({ length: 0, item: () => null, [Symbol.iterator]: [][Symbol.iterator] }),
});

Object.defineProperty(Element.prototype, 'getBoundingClientRect', {
  configurable: true,
  value: () => emptyRect,
});

Object.defineProperty(window, 'matchMedia', {
  configurable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent: () => false,
  }),
});

// Only the shared axios instance is reset here. Specs reset the auth store
// themselves, because importing it from this setup file would evaluate the real
// service module before a spec registers its `vi.mock`, defeating the mock.
afterEach(() => {
  resetApiAuth();
  // Vitest runs without globals here, so Testing Library cannot register its own
  // cleanup; without this every render piles up in document.body and later
  // queries match several copies of the same element.
  cleanup();
});
