// The watchdog thread, the thread sample, the stack scan and the stall dump (roadmap H5): see monitor.h.
#include "core/watchdog/monitor.h"

#include <dbghelp.h>

#include <cstddef>
#include <cstdio>

namespace gms {
namespace watchdog {

// ---------------------------------------------------------------------------------------------- the sample

bool SampleThread(HANDLE thread, std::uint32_t threadId, ThreadSample* out, std::string* err) {
  out->ok = false;
  out->threadId = threadId;
  out->stackDwords = 0;
  if (!thread || threadId == GetCurrentThreadId()) {
    if (err) *err = "no thread to sample";
    return false;
  }
  if (SuspendThread(thread) == static_cast<DWORD>(-1)) {
    if (err) *err = "SuspendThread failed (" + std::to_string(GetLastError()) + ")";
    return false;
  }
  // from here until ResumeThread: no heap, no lock (the thread may hold either)
  out->context.ContextFlags = CONTEXT_FULL;
  const BOOL gotContext = GetThreadContext(thread, &out->context);  // also waits for the suspension to take hold
  DWORD contextError = gotContext ? 0 : GetLastError();
  if (gotContext) {
    // the stack from esp up to the end of its committed region (the stack's base), at most kMaxStack dwords
    const std::uintptr_t esp = out->context.Esp;
    MEMORY_BASIC_INFORMATION mbi;
    if (VirtualQuery(reinterpret_cast<const void*>(esp), &mbi, sizeof mbi) == sizeof mbi && mbi.State == MEM_COMMIT) {
      const std::uintptr_t end = reinterpret_cast<std::uintptr_t>(mbi.BaseAddress) + mbi.RegionSize;
      std::size_t bytes = static_cast<std::size_t>(end - esp) & ~static_cast<std::size_t>(3);
      if (bytes > sizeof out->stack) bytes = sizeof out->stack;
      SIZE_T got = 0;
      if (ReadProcessMemory(GetCurrentProcess(), reinterpret_cast<const void*>(esp), out->stack, bytes, &got))
        out->stackDwords = static_cast<int>(got / sizeof(std::uint32_t));
    }
  }
  ResumeThread(thread);
  if (!gotContext) {
    if (err) *err = "GetThreadContext failed (" + std::to_string(contextError) + ")";
    return false;
  }
  out->ok = true;
  return true;
}

// ---------------------------------------------------------------------------------------------- code ranges

bool CodeRanges::AddModule(HMODULE module, const std::string& name) {
  if (!module) return false;
  const auto* base = reinterpret_cast<const unsigned char*>(module);
  const auto* dos = reinterpret_cast<const IMAGE_DOS_HEADER*>(base);
  if (dos->e_magic != IMAGE_DOS_SIGNATURE) return false;
  const auto* nt = reinterpret_cast<const IMAGE_NT_HEADERS*>(base + dos->e_lfanew);
  if (nt->Signature != IMAGE_NT_SIGNATURE) return false;
  const bool exe = (nt->FileHeader.Characteristics & IMAGE_FILE_DLL) == 0;
  const IMAGE_SECTION_HEADER* sec = IMAGE_FIRST_SECTION(nt);
  bool any = false;
  for (unsigned i = 0; i < nt->FileHeader.NumberOfSections; ++i, ++sec) {
    if (!(sec->Characteristics & IMAGE_SCN_MEM_EXECUTE)) continue;
    Range r;
    r.base = static_cast<std::uint32_t>(reinterpret_cast<std::uintptr_t>(base));
    r.lo = r.base + sec->VirtualAddress;
    r.hi = r.lo + (sec->Misc.VirtualSize ? sec->Misc.VirtualSize : sec->SizeOfRawData);
    r.exe = exe;
    r.module = name;
    ranges_.push_back(r);
    any = true;
  }
  return any;
}

const CodeRanges::Range* CodeRanges::Find(std::uint32_t v) const {
  for (const Range& r : ranges_)
    if (v >= r.lo && v < r.hi) return &r;
  return nullptr;
}

bool CodeRanges::IsReturnAddress(std::uint32_t v) const {
  const Range* r = Find(v);
  if (!r || v < r->lo + 7) return false;
  // the call instruction that ends at v: E8 rel32, or FF /2 (ModRM 0x10-0x17, 0x50-0x57, 0x90-0x97, 0xD0-0xD7) by
  // its length: opcode at q[-length], ModRM after it (rm 4 = a SIB byte follows, mod 0 rm 5 = disp32)
  const auto* q = reinterpret_cast<const unsigned char*>(static_cast<std::uintptr_t>(v));
  auto in = [](unsigned char m, unsigned char lo) { return m >= lo && m <= lo + 7; };
  if (q[-5] == 0xE8) return true;  // call rel32
  if (q[-2] == 0xFF && (in(q[-1], 0xD0) || (in(q[-1], 0x10) && q[-1] != 0x14 && q[-1] != 0x15)))
    return true;  // call reg, call [reg]
  if (q[-3] == 0xFF && ((in(q[-2], 0x50) && q[-2] != 0x54) || q[-2] == 0x14)) return true;  // [reg+d8], [sib]
  if (q[-4] == 0xFF && q[-3] == 0x54) return true;                                          // [sib+d8]
  if (q[-6] == 0xFF && (q[-5] == 0x15 || (in(q[-5], 0x90) && q[-5] != 0x94))) return true;  // [d32], [reg+d32]
  if (q[-7] == 0xFF && (q[-6] == 0x94 || q[-6] == 0x14)) return true;                       // [sib+d32]
  return false;
}

std::string CodeRanges::Name(std::uint32_t v) const {
  char buf[96];
  const Range* r = Find(v);
  if (!r || r->exe)
    std::snprintf(buf, sizeof buf, "%08X", static_cast<unsigned>(v));
  else
    std::snprintf(buf, sizeof buf, "%08X (%s+0x%X)", static_cast<unsigned>(v), r->module.c_str(),
                  static_cast<unsigned>(v - r->base));
  return buf;
}

std::vector<std::uint32_t> ReturnAddresses(const ThreadSample& s, const CodeRanges& code, int max) {
  std::vector<std::uint32_t> out;
  for (int i = 0; i < s.stackDwords && static_cast<int>(out.size()) < max; ++i)
    if (code.IsReturnAddress(s.stack[i])) out.push_back(s.stack[i]);
  return out;
}

// ---------------------------------------------------------------------------------------------- the dump

namespace {

using MiniDumpWriteDumpFn = BOOL(WINAPI*)(HANDLE, DWORD, HANDLE, MINIDUMP_TYPE, PMINIDUMP_EXCEPTION_INFORMATION,
                                          PMINIDUMP_USER_STREAM_INFORMATION, PMINIDUMP_CALLBACK_INFORMATION);

MiniDumpWriteDumpFn g_writeDump;
bool g_dbghelpTried;
std::string g_dbghelpError;

struct DumpJob {
  std::string path;
  ThreadSample sample;
  DumpKind kind = DumpKind::kSmall;
  EXCEPTION_RECORD record{};
  EXCEPTION_POINTERS pointers{};
  bool ok = false;
  std::string err;
};

DWORD WINAPI DumpThread(void* p) {
  DumpJob* job = static_cast<DumpJob*>(p);
  HANDLE file = CreateFileA(job->path.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
  if (file == INVALID_HANDLE_VALUE) {
    job->err = "can't create " + job->path + " (" + std::to_string(GetLastError()) + ")";
    return 0;
  }
  const int type = job->kind == DumpKind::kFull
                       ? (MiniDumpWithFullMemory | MiniDumpWithHandleData | MiniDumpWithThreadInfo |
                          MiniDumpWithUnloadedModules)
                       : (MiniDumpWithIndirectlyReferencedMemory | MiniDumpWithThreadInfo | MiniDumpWithUnloadedModules);
  MINIDUMP_EXCEPTION_INFORMATION mei{};
  mei.ThreadId = job->sample.threadId;
  mei.ExceptionPointers = &job->pointers;
  mei.ClientPointers = FALSE;
  job->ok = g_writeDump(GetCurrentProcess(), GetCurrentProcessId(), file, static_cast<MINIDUMP_TYPE>(type),
                        job->sample.ok ? &mei : nullptr, nullptr, nullptr) != FALSE;
  if (!job->ok) job->err = "MiniDumpWriteDump failed (" + std::to_string(GetLastError()) + ")";
  CloseHandle(file);
  return 0;
}

}  // namespace

bool LoadDbgHelp(std::string* err) {
  if (!g_dbghelpTried) {
    g_dbghelpTried = true;
    wchar_t dir[MAX_PATH];
    const UINT n = GetSystemDirectoryW(dir, MAX_PATH);  // System32; a WOW64 process is redirected to SysWOW64
    HMODULE dbghelp = nullptr;
    if (n > 0 && n < MAX_PATH - 16) {
      std::wstring path = std::wstring(dir) + L"\\dbghelp.dll";
      dbghelp = LoadLibraryW(path.c_str());
    }
    if (dbghelp) g_writeDump = reinterpret_cast<MiniDumpWriteDumpFn>(GetProcAddress(dbghelp, "MiniDumpWriteDump"));
    if (!g_writeDump)
      g_dbghelpError = dbghelp ? "dbghelp.dll has no MiniDumpWriteDump"
                               : "can't load dbghelp.dll from System32 (" + std::to_string(GetLastError()) + ")";
  }
  if (!g_writeDump && err) *err = g_dbghelpError;
  return g_writeDump != nullptr;
}

bool WriteDump(const std::string& path, const ThreadSample& s, DumpKind kind, int timeoutMs, std::string* err) {
  if (kind == DumpKind::kNone) {
    if (err) *err = "no dump (off)";
    return false;
  }
  if (!LoadDbgHelp(err)) return false;
  DumpJob* job = new DumpJob;
  job->path = path;
  job->sample = s;
  job->kind = kind;
  job->record.ExceptionCode = kStallExceptionCode;
  job->record.ExceptionFlags = EXCEPTION_NONCONTINUABLE;
  job->record.ExceptionAddress = reinterpret_cast<void*>(static_cast<std::uintptr_t>(s.Eip()));
  job->pointers.ExceptionRecord = &job->record;
  job->pointers.ContextRecord = &job->sample.context;
  HANDLE worker = CreateThread(nullptr, 0, &DumpThread, job, 0, nullptr);
  if (!worker) {
    if (err) *err = "CreateThread failed (" + std::to_string(GetLastError()) + ")";
    delete job;
    return false;
  }
  if (WaitForSingleObject(worker, timeoutMs > 0 ? static_cast<DWORD>(timeoutMs) : INFINITE) != WAIT_OBJECT_0) {
    CloseHandle(worker);
    if (err) *err = "the dump did not finish within " + std::to_string(timeoutMs) + " ms";
    return false;  // the job stays allocated: the worker may still write it
  }
  CloseHandle(worker);
  const bool ok = job->ok;
  if (!ok && err) *err = job->err;
  delete job;
  return ok;
}

// ---------------------------------------------------------------------------------------------- the monitor

Monitor::~Monitor() { Stop(); }

std::uint32_t Monitor::NowMs() const {
  LARGE_INTEGER c;
  QueryPerformanceCounter(&c);
  return static_cast<std::uint32_t>((c.QuadPart - t0_.QuadPart) * 1000 / freq_.QuadPart);
}

bool Monitor::Start(const Options& o, std::uint32_t threadId, OnStall onStall, std::string* err) {
  if (thread_) {
    if (err) *err = "already running";
    return false;
  }
  o_ = o;
  if (o_.pollMs <= 0) o_.pollMs = 200;
  onStall_ = std::move(onStall);
  QueryPerformanceFrequency(&freq_);
  QueryPerformanceCounter(&t0_);
  watchedId_ = threadId;
  watched_ = OpenThread(THREAD_SUSPEND_RESUME | THREAD_GET_CONTEXT | THREAD_QUERY_INFORMATION, FALSE, threadId);
  if (!watched_) {
    if (err) *err = "OpenThread(" + std::to_string(threadId) + ") failed (" + std::to_string(GetLastError()) + ")";
    return false;
  }
  stop_ = CreateEventW(nullptr, TRUE, FALSE, nullptr);
  thread_ = stop_ ? CreateThread(nullptr, 0, &Monitor::ThreadMain, this, 0, nullptr) : nullptr;
  if (!thread_) {
    if (err) *err = "CreateThread failed (" + std::to_string(GetLastError()) + ")";
    if (stop_) CloseHandle(stop_);
    CloseHandle(watched_);
    stop_ = watched_ = nullptr;
    return false;
  }
  return true;
}

void Monitor::Stop() {
  if (!thread_) return;
  SetEvent(stop_);
  if (GetCurrentThreadId() != GetThreadId(thread_)) WaitForSingleObject(thread_, 5000);
  CloseHandle(thread_);
  CloseHandle(stop_);
  CloseHandle(watched_);
  thread_ = stop_ = watched_ = nullptr;
}

void Monitor::Beat(std::uint32_t ticks) {
  ticks_.store(ticks);
  armed_.store(true);
}

void Monitor::Enter(const char* step) {
  stepSinceMs_.store(NowMs());
  step_.store(step);
}

DWORD WINAPI Monitor::ThreadMain(void* self) {
  static_cast<Monitor*>(self)->Run();
  return 0;
}

void Monitor::Run() {
  StallDetector detector(o_.thresholdMs);
  while (WaitForSingleObject(stop_, static_cast<DWORD>(o_.pollMs)) == WAIT_TIMEOUT) {
    const std::uint32_t now = NowMs();
    if (!detector.Check(now, ticks_.load(), armed_.load())) continue;
    stalls_.fetch_add(1);
    Stall st;
    st.stalledMs = detector.SinceChangeMs(now);
    st.ticks = detector.LastTicks();
    st.step = step_.load();
    st.stepMs = st.step ? static_cast<std::int64_t>(now - stepSinceMs_.load()) : 0;
    SampleThread(watched_, watchedId_, &sample_, nullptr);
    st.sample = sample_;
    if (onStall_) onStall_(st);
  }
}

}  // namespace watchdog
}  // namespace gms
