import { defineNuxtConfig } from "nuxt/config";

// Fork-only Nuxt layer (.fork/PLAN.md §3). Nuxt auto-registers every ~~/layers/* directory,
// so frontend/nuxt.config.ts stays untouched. Components are not auto-imported upstream
// (components.dirs: []), so import everything explicitly.
export default defineNuxtConfig({});
