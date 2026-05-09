import { beforeEach, vi } from "vitest";

let storageBacking = {};

const browserMock = {
  storage: {
    sync: {
      get: vi.fn(async (keys) => {
        if (typeof keys === "string") {
          return storageBacking[keys] !== undefined
            ? { [keys]: storageBacking[keys] }
            : {};
        }
        if (Array.isArray(keys)) {
          const out = {};
          for (const k of keys) {
            if (storageBacking[k] !== undefined) out[k] = storageBacking[k];
          }
          return out;
        }
        return { ...storageBacking };
      }),
      set: vi.fn(async (items) => {
        Object.assign(storageBacking, items);
      }),
      clear: vi.fn(async () => {
        storageBacking = {};
      }),
    },
  },
  runtime: {
    openOptionsPage: vi.fn(async () => {}),
    getURL: vi.fn((path) => `moz-extension://test-extension/${path}`),
    onInstalled: { addListener: vi.fn() },
  },
  tabs: {
    query: vi.fn(async () => [{ url: "https://example.com" }]),
    create: vi.fn(async () => ({})),
  },
  contextMenus: {
    create: vi.fn(),
    onClicked: { addListener: vi.fn() },
  },
  notifications: {
    create: vi.fn(async () => "notification-id"),
  },
  permissions: {
    contains: vi.fn(async () => true),
    request: vi.fn(async () => true),
  },
};

globalThis.browser = browserMock;
globalThis.chrome = browserMock;

beforeEach(async () => {
  storageBacking = {};
  vi.clearAllMocks();
  browserMock.permissions.contains.mockResolvedValue(true);
  browserMock.permissions.request.mockResolvedValue(true);
});
