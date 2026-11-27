// Tabs injected into frontend/pages/item/[id]/index.vue through a single spread (a seam).
// Shape mirrors that file's tabs array. Populated in task P4.
import type { Ref } from "vue";

export type CustomItemTab = {
  id: string;
  name: string;
  to: string;
};

// eslint-disable-next-line @typescript-eslint/no-unused-vars -- used once P4 adds the Projects tab
export function customItemTabs(itemId: Readonly<Ref<string>>): CustomItemTab[] {
  return [];
}
