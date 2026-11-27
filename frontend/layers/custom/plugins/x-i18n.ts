// Merges the layer's locale files (keys under `x.*`) into the app's vue-i18n instance, so
// upstream's frontend/locales/*.json never change (.fork/PLAN.md R8).
// enforce: "post" makes this run after upstream's plugins/i18n.ts, which provides $i18nGlobal.
import type { Composer } from "vue-i18n";

const modules = import.meta.glob<Record<string, unknown>>("../locales/*.json", { eager: true, import: "default" });

export default defineNuxtPlugin({
  name: "x-i18n",
  enforce: "post",
  setup(nuxtApp) {
    const i18n = nuxtApp.$i18nGlobal as Composer | undefined;
    if (!i18n) {
      console.warn("[x-i18n] $i18nGlobal not provided; custom strings unavailable");
      return;
    }
    for (const [path, messages] of Object.entries(modules)) {
      const locale = path.slice(path.lastIndexOf("/") + 1, -".json".length);
      // Upstream registers its messages as ES module namespace objects, which are read-only, so
      // mergeLocaleMessage would throw. Replace with a plain object combining both instead.
      i18n.setLocaleMessage(locale, { ...i18n.getLocaleMessage(locale), ...messages });
    }
  },
});
