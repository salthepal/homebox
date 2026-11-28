// Currency formatting that follows the group's configured currency (same source as the
// rest of the app), falling back to USD until it has loaded.
import { shallowRef } from "vue";
import { useFormatCurrency } from "~/composables/use-formatters";
import { formatMoney } from "../lib/projects";

export function useMoney(): (n: number | null | undefined) => string {
  const fmt = shallowRef<((v: number | string) => string) | null>(null);
  useFormatCurrency()
    .then(f => {
      fmt.value = f;
    })
    .catch(() => {
      // keep the USD fallback
    });
  return n => (fmt.value ? fmt.value(n ?? 0) : formatMoney(n));
}
