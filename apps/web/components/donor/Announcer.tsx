/**
 * Live regions stay mounted for the life of the page: assistive technology only
 * announces changes to a region it already knows about, so a region mounted together
 * with its first message is often read late or not at all.
 */
export function Announcer({
  status,
  alert,
  id,
  statusShownElsewhere,
}: {
  status?: string;
  alert?: string;
  id?: string;
  /** The status is also on the page, so it is announced without showing it twice. */
  statusShownElsewhere?: boolean;
}) {
  return (
    <>
      <p role="status" className={statusShownElsewhere ? "liveStatus visuallyHidden" : "liveStatus"}>
        {status}
      </p>
      <p role="alert" id={id} className="errorMessage">
        {alert}
      </p>
    </>
  );
}
