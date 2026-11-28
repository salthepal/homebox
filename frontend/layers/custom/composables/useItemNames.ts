// Resolves item (entity) IDs to names for display, with a per-session cache.
// Unknown/deleted items resolve to null so callers can show a fallback.
export function useItemNames() {
  const api = useUserApi();
  const names = useState<Record<string, string | null>>("x-item-names", () => ({}));
  const pending = new Set<string>();

  async function resolve(ids: Array<string | null | undefined>) {
    const todo = [...new Set(ids.filter((id): id is string => !!id))].filter(
      id => !(id in names.value) && !pending.has(id)
    );
    todo.forEach(id => pending.add(id));
    await Promise.all(
      todo.map(async id => {
        const resp = await api.items.get(id);
        names.value = { ...names.value, [id]: resp.error ? null : resp.data.name };
        pending.delete(id);
      })
    );
  }

  function nameOf(id: string | null | undefined): string {
    if (!id) return "";
    const n = names.value[id];
    return n === undefined ? "…" : (n ?? "(deleted item)");
  }

  return { names, resolve, nameOf };
}
