// Typed client for the fork's /api/v1/x endpoints. Hand-written on purpose: custom handlers
// are not in upstream's swagger, so frontend/lib/api/types is never regenerated (R7).
import { BaseAPI, route } from "~~/lib/api/base";

export type XPing = {
  ok: boolean;
  groupId: string;
  schema: string;
};

export class CustomApi extends BaseAPI {
  ping() {
    return this.http.get<XPing>({ url: route("/x/ping") });
  }
}

/** Custom API client sharing upstream's authenticated request pipeline (auth, X-Tenant, 401 handling). */
export function useCustomApi(): CustomApi {
  const user = useUserApi();
  return new CustomApi(user.http, user.attachmentToken);
}
