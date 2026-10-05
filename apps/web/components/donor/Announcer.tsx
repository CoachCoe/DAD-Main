/**
 * Live regions stay mounted for the life of the page: assistive technology only
 * announces changes to a region it already knows about, so a region mounted together
 * with its first message is often read late or not at all.
 */
export function Announcer({ status, alert, id }: { status?: string; alert?: string; id?: string }) {
  return <>
    <p role="status" className="liveStatus">{status}</p>
    <p role="alert" id={id} className="errorMessage">{alert}</p>
  </>;
}
