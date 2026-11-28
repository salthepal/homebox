// Currency formatting that follows the group's configured currency (same source as the
// rest of the app), falling back to USD until it has loaded.
import { shallowRef } from "vue";
import { useFormatCurrency } from "~/composables/use-formatters";
import { formatMoney } from "../lib/projects";

type Formatter = (v: number | string) => string;

// One in-flight load shared by every caller (a board renders many cards at once).
let loading: Promise<Formatter> | null = null;

export function useMoney(): (n: number | null | undefined) => string {
  const fmt = shallowRef<Formatter | null>(null);
  loading ??= useFormatCurrency().catch(err => {
    loading = null; // allow a retry on the next call
    throw err;
  });
  loading
    .then(f => {
      fmt.value = f;
    })
    .catch(() => {
      // keep the USD fallback
    });
  return n => (fmt.value ? fmt.value(n ?? 0) : formatMoney(n));
}
