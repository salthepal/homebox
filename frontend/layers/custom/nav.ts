// Nav entries injected into frontend/layouts/default.vue through a single spread (a seam).
// The shape mirrors that file's nav array; ids >= 100 avoid clashing with upstream's.
import type { Component, ComputedRef } from "vue";
import type { RouteLocationNormalizedLoaded } from "vue-router";
import MdiClipboardListOutline from "~icons/mdi/clipboard-list-outline";
import MdiCalendarSync from "~icons/mdi/calendar-sync";

export type CustomNavItem = {
  icon: Component;
  active: ComputedRef<boolean>;
  id: number;
  name: ComputedRef<string>;
  to: string;
};

export function customNavItems(t: (key: string) => string, route: RouteLocationNormalizedLoaded): CustomNavItem[] {
  return [
    {
      icon: MdiClipboardListOutline,
      id: 100,
      active: computed(() => route.path.startsWith("/x/projects")),
      name: computed(() => t("x.menu.projects")),
      to: "/x/projects",
    },
    {
      icon: MdiCalendarSync,
      id: 101,
      active: computed(() => route.path.startsWith("/x/calendar")),
      name: computed(() => t("x.menu.calendar")),
      to: "/x/calendar",
    },
  ];
}
