// The rate-limit identity: an IPv4 address, or the /64 of an IPv6 one, which a single
// subscriber typically holds whole. The API buckets clients the same way.
function key(r) {
    const addr = r.variables.remote_addr;
    const v4 = /^(?:::ffff:)?(\d+\.\d+\.\d+\.\d+)$/i.exec(addr);
    if (v4) return v4[1];
    const halves = addr.split('::').map((part) => (part ? part.split(':') : []));
    // An embedded IPv4 tail (64:ff9b::192.0.2.1) fills two groups.
    const width = (groups) => groups.reduce((n, group) => n + (group.includes('.') ? 2 : 1), 0);
    const groups = halves.length < 2 ? halves[0]
        : halves[0].concat(new Array(8 - width(halves[0]) - width(halves[1])).fill('0'), halves[1]);
    return groups.slice(0, 4).map((group) => parseInt(group, 16).toString(16)).join(':');
}

export default { key };
