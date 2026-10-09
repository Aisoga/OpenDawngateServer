"""Reads a CrashRpt minidump of Dawngate.exe: the exception, the x86 registers and the likely return addresses on the
faulting thread's stack (B9). Also reads the match host's watchdog.dmp (H5: the stalled engine thread as the
exception thread, code E0057A11).

CrashRpt writes crashdump.dmp next to crashrpt.xml (%LOCALAPPDATA%\\CrashRpt\\UnsentCrashReports\\
Dawngate_1.0.903.854\\<guid>\\). ExceptionAddress alone often lands in a shared
helper (b9_run1: a string constructor); the stack shows who called it. Every stack dword that points into .text is
printed, so stale values show up too; check each candidate in Ghidra (a call instruction must precede it). Since
H5-live a dword inside another module of the dump (a DLL such as the host's PhysXLoader.dll = dgsim.dll, kernel32,
ntdll) is printed too, as module+offset, and the stack comes from the dump's memory list when the thread's own stack
descriptor has no data (watchdog.dmp, written with MiniDumpWithIndirectlyReferencedMemory: every thread's stack
Rva is 0 there, and before H5-live this script read the file's header instead).
Stdlib only; never copy the dump into the repo (it can hold memory of the whole game).

Usage:
    .venv/Scripts/python -I re/scripts/minidump_stack.py <crashdump.dmp | watchdog.dmp> [lo hi]
  lo/hi: hex range of plausible return addresses (default: Dawngate.exe 854's .text, 0x00401000-0x011AB0FB).
"""
import struct
import sys


def memory_ranges(d, streams):
    """(start, size, file offset) of every captured memory range: MemoryListStream (5) and Memory64ListStream (9)."""
    out = []
    for _size, srva in streams.get(5, []):
        n, = struct.unpack_from('<I', d, srva)
        for k in range(n):
            start, size, rva = struct.unpack_from('<QII', d, srva + 4 + 16 * k)
            out.append((start, size, rva))
    for _size, srva in streams.get(9, []):
        n, rva = struct.unpack_from('<QQ', d, srva)
        for k in range(n):
            start, size = struct.unpack_from('<QQ', d, srva + 16 + 16 * k)
            out.append((start, size, rva))
            rva += size
    return out


def modules(d, streams):
    """(base, size, file name) from ModuleListStream (4); MINIDUMP_MODULE is 108 bytes."""
    out = []
    for _size, srva in streams.get(4, []):
        n, = struct.unpack_from('<I', d, srva)
        for k in range(n):
            base, size, _sum, _stamp, name_rva = struct.unpack_from('<QIIII', d, srva + 4 + 108 * k)
            length, = struct.unpack_from('<I', d, name_rva)
            name = d[name_rva + 4:name_rva + 4 + length].decode('utf-16-le', 'replace')
            out.append((base, size, name.replace('/', '\\').split('\\')[-1]))
    return out


def main():
    if len(sys.argv) < 2:
        print(__doc__)
        return 2
    lo = int(sys.argv[2], 16) if len(sys.argv) > 2 else 0x00401000
    hi = int(sys.argv[3], 16) if len(sys.argv) > 3 else 0x011AB0FB
    d = open(sys.argv[1], 'rb').read()
    sig, _ver, nstreams, rva = struct.unpack_from('<4sIII', d, 0)
    if sig != b'MDMP':
        print('not a minidump', file=sys.stderr)
        return 2
    streams = {}
    for i in range(nstreams):
        t, size, srva = struct.unpack_from('<III', d, rva + 12 * i)
        streams.setdefault(t, []).append((size, srva))
    if 6 not in streams:  # ExceptionStream
        print('no exception stream', file=sys.stderr)
        return 2

    _size, srva = streams[6][0]
    tid, = struct.unpack_from('<I', d, srva)
    # MINIDUMP_EXCEPTION: code, flags, record (u64), address (u64), nparams, align, 15 x u64 params
    code, _flags, _rec, addr, nparams = struct.unpack_from('<IIQQI', d, srva + 8)
    params = struct.unpack_from('<15Q', d, srva + 8 + 32)
    _ctx_size, ctx_rva = struct.unpack_from('<II', d, srva + 8 + 152)
    print('thread %d: exception %08x at %08x, parameters %s' %
          (tid, code, addr, ' '.join('%x' % p for p in params[:nparams])))
    # x86 CONTEXT: ContextFlags, Dr0-3/6/7, FloatSave (112), Gs Fs Es Ds, Edi Esi Ebx Edx Ecx Eax Ebp Eip Cs EFlags Esp Ss
    edi, esi, ebx, edx, ecx, eax, ebp, eip, _cs, _efl, esp, _ss = struct.unpack_from('<12I', d, ctx_rva + 4 + 24 + 112 + 16)
    print('eax %08x ebx %08x ecx %08x edx %08x esi %08x edi %08x ebp %08x esp %08x eip %08x' %
          (eax, ebx, ecx, edx, esi, edi, ebp, esp, eip))

    mods = modules(d, streams)
    main_base = min((m[0] for m in mods if m[2].lower().endswith('.exe')), default=None)

    def where(v):
        """'module+offset' for a dword inside a module other than the exe, else ''."""
        for base, size, name in mods:
            if base <= v < base + size and base != main_base:
                return '%s+0x%x' % (name, v - base)
        return ''

    if where(eip):
        print('eip %08x is in %s' % (eip, where(eip)))
    for _size, srva in streams.get(3, []):  # ThreadListStream: MINIDUMP_THREAD is 48 bytes
        n, = struct.unpack_from('<I', d, srva)
        for j in range(n):
            t = struct.unpack_from('<IIIIQQII', d, srva + 4 + 48 * j)
            if t[0] != tid:
                continue
            start, size, mrva = t[5], t[6], t[7]
            source = 'the thread\'s stack descriptor'
            if mrva == 0:  # watchdog.dmp: the stack is only in the memory list
                for mstart, msize, moff in memory_ranges(d, streams):
                    if mstart <= start and start + size <= mstart + msize:
                        mrva = moff + (start - mstart)
                        source = 'the memory list (the thread\'s stack descriptor has no data)'
                        break
                else:
                    print('stack %08x..%08x: not captured in the dump' % (start, start + size))
                    continue
            print('stack %08x..%08x (%d bytes captured, from %s); code addresses from esp up:' %
                  (start, start + size, size, source))
            for a in range(max(esp, start), start + size - 3, 4):
                v, = struct.unpack_from('<I', d, mrva + a - start)
                if lo <= v < hi:
                    print('  [%08x] esp+%04x  %08x' % (a, a - esp, v))
                else:
                    w = where(v)
                    if w:
                        print('  [%08x] esp+%04x  %08x  %s' % (a, a - esp, v, w))
    return 0


if __name__ == '__main__':
    sys.exit(main())
