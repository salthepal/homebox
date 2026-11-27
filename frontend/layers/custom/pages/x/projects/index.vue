<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseSectionHeader from "@/components/Base/SectionHeader.vue";
  import { useCustomApi } from "../../../lib/api/client";

  const { t } = useI18n();
  const api = useCustomApi();

  definePageMeta({
    middleware: ["auth"],
  });
  useHead({
    title: "HomeBox | " + t("x.projects.title"),
  });

  const { data: ping } = useAsyncData("x-ping", async () => {
    const resp = await api.ping();
    return resp.error ? null : resp.data;
  });
</script>

<template>
  <div>
    <BaseContainer class="flex flex-col gap-4">
      <BaseSectionHeader> {{ t("x.projects.title") }} </BaseSectionHeader>
      <p class="text-muted-foreground">{{ t("x.projects.coming_soon") }}</p>
      <p class="text-sm" :class="ping?.ok ? 'text-green-600' : 'text-destructive'" data-testid="x-backend-status">
        {{ ping?.ok ? t("x.projects.backend_ok") : t("x.projects.backend_error") }}
      </p>
    </BaseContainer>
  </div>
</template>
