export const siteOf = (instance: { instance_id: string; site_id?: string }) =>
  instance.site_id || instance.instance_id;
