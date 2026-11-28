// Tabs injected into frontend/pages/item/[id]/index.vue through a single spread (a seam).
// Shape mirrors that file's tabs array (`name` is an i18n key, resolved with $t there).
import type { Ref } from "vue";

export type CustomItemTab = {
  id: string;
  name: string;
  to: string;
};

export function customItemTabs(itemId: Readonly<Ref<string>>): CustomItemTab[] {
  return [
    {
      id: "x-projects",
      name: "x.projects.item_tab.title",
      to: `/item/${itemId.value}/projects`,
    },
  ];
}
