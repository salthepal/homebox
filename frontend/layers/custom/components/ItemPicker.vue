<script setup lang="ts">
  // Search-as-you-type picker over the user's items. Emits `pick` with the chosen item.
  import { useI18n } from "vue-i18n";
  import { Input } from "@/components/ui/input";

  const props = defineProps<{ exclude?: string[]; placeholder?: string }>();
  const emit = defineEmits<{ (e: "pick", item: { id: string; name: string }): void }>();

  const { t } = useI18n();
  const api = useUserApi();

  const query = ref("");
  const results = ref<Array<{ id: string; name: string }>>([]);
  const searched = ref(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let seq = 0;

  async function search() {
    const mine = ++seq;
    const q = query.value.trim();
    if (!q) {
      results.value = [];
      searched.value = false;
      return;
    }
    const resp = await api.items.getAll({ q, page: 1, pageSize: 8 });
    if (mine !== seq) return; // a newer search superseded this one
    searched.value = true;
    results.value = resp.error
      ? []
      : resp.data.items.filter(i => !props.exclude?.includes(i.id)).map(i => ({ id: i.id, name: i.name }));
  }

  watch(query, () => {
    clearTimeout(timer);
    timer = setTimeout(search, 250);
  });
  onBeforeUnmount(() => clearTimeout(timer));

  function pick(item: { id: string; name: string }) {
    emit("pick", item);
    query.value = "";
    results.value = [];
    searched.value = false;
  }
</script>

<template>
  <div class="relative">
    <Input v-model="query" :placeholder="placeholder ?? t('x.projects.items.search')" data-testid="x-item-search" />
    <ul
      v-if="results.length || searched"
      class="absolute z-20 mt-1 max-h-56 w-full overflow-auto rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
    >
      <li v-if="!results.length" class="px-2 py-1.5 text-sm text-muted-foreground">
        {{ t("x.projects.items.no_results") }}
      </li>
      <li v-for="item in results" :key="item.id">
        <button
          type="button"
          class="w-full rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent hover:text-accent-foreground"
          @click="pick(item)"
        >
          {{ item.name }}
        </button>
      </li>
    </ul>
  </div>
</template>
