// physcook.exe (MAPPHYS): cooks a level's PhysX triangle mesh with the game's own PhysXCooking.dll (PhysX 2.8.4.4,
// x86), so that the level loader (Dawngate.exe 0x008E65A0, NxPhysicsSDK::createTriangleMesh) reads it as it reads
// the shipped SM_Map_v5.bin.
//
//   physcook <PhysXCooking.dll> <in.dgmi> <out.bin> [--axis <n>] [--hint-speed]
//   physcook <PhysXCooking.dll> --params
//
// <in.dgmi> is the mesh in gms/core/physx/cook.h's input format (SM_Map_v5's own triangles for a check against
// the shipped file, or a level's floor and walls). The DLL is loaded by its full path (a copy taken out of a working
// copy of the game); the tool calls NxInitCooking, NxGetCookingParams, NxCookTriangleMesh into a
// memory stream and NxCloseCooking, then writes the cooked bytes. --params prints the cooking parameters and stops.
// Experiment options: --axis sets NxTriangleMeshDesc::heightFieldVerticalAxis (default 255, NX_NOT_HEIGHTFIELD;
// any other value makes 2.8.4.4 skip its mesh cleaning, PhysXCooking.dll 0x1004A538), --hint-speed sets
// NxCookingParams::hintCollisionSpeed. Exit codes: 0 cooked, 1 cooking failed (the DLL's errors are printed),
// 2 usage, input or DLL error. The process opens no socket and needs no game process (the DLL imports WSOCK32, but
// cooking calls none of it).
#include <windows.h>

#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <string>
#include <vector>

#include "core/physx/cook.h"

namespace {

using gms::physx::MemoryStream;
using gms::physx::MeshInput;
using gms::physx::OutputStream28;
using gms::physx::Stream28;
using gms::physx::TriangleMeshDesc28;

struct CookingParams28 {  // NxCookingParams: NxPlatform targetPlatform, NxReal skinWidth, bool hintCollisionSpeed
  uint32_t targetPlatform;
  float skinWidth;
  uint32_t hintCollisionSpeed;  // a bool and its padding
};

using InitCookingFn = bool(__cdecl*)(void* allocator, OutputStream28* output);
using CloseCookingFn = void(__cdecl*)();
using CookTriangleMeshFn = bool(__cdecl*)(const TriangleMeshDesc28& desc, Stream28& stream);
using GetCookingParamsFn = const CookingParams28*(__cdecl*)();
using SetCookingParamsFn = bool(__cdecl*)(const CookingParams28& params);

class PrintingOutput : public OutputStream28 {
 public:
  int errors = 0;
  void reportError(int code, const char* message, const char* file, int line) override {
    ++errors;
    std::fprintf(stderr, "PhysXCooking error %d: %s (%s:%d)\n", code, message ? message : "", file ? file : "", line);
  }
  int reportAssertViolation(const char* message, const char* file, int line) override {
    ++errors;
    std::fprintf(stderr, "PhysXCooking assert: %s (%s:%d)\n", message ? message : "", file ? file : "", line);
    return 0;  // NX_AR_CONTINUE
  }
  void print(const char* message) override { std::fprintf(stderr, "PhysXCooking: %s", message ? message : ""); }
};

bool ReadFile(const char* path, std::vector<uint8_t>& out) {
  FILE* f = std::fopen(path, "rb");
  if (!f) return false;
  out.clear();
  uint8_t buf[65536];
  size_t n;
  while ((n = std::fread(buf, 1, sizeof buf, f)) > 0) out.insert(out.end(), buf, buf + n);
  std::fclose(f);
  return true;
}

bool WriteFile(const char* path, const std::vector<uint8_t>& b) {
  FILE* f = std::fopen(path, "wb");
  if (!f) return false;
  const bool ok = std::fwrite(b.data(), 1, b.size(), f) == b.size();
  return std::fclose(f) == 0 && ok;
}

template <class Fn>
Fn Proc(HMODULE dll, const char* name) {
  return reinterpret_cast<Fn>(reinterpret_cast<void*>(GetProcAddress(dll, name)));
}

int Usage() {
  std::fprintf(stderr, "usage: physcook <PhysXCooking.dll> <in.dgmi> <out.bin> [--axis <n>] [--hint-speed]\n"
                       "       physcook <PhysXCooking.dll> --params\n");
  return 2;
}

}  // namespace

int main(int argc, char** argv) {
  if (argc < 3) return Usage();
  const bool paramsOnly = argc == 3 && std::string(argv[2]) == "--params";
  if (!paramsOnly && argc < 4) return Usage();
  uint32_t axis = gms::physx::kNotHeightField;
  bool hintSpeed = false;
  for (int i = 4; !paramsOnly && i < argc; ++i) {
    const std::string a = argv[i];
    if (a == "--axis" && i + 1 < argc) {
      axis = static_cast<uint32_t>(std::strtoul(argv[++i], nullptr, 0));
    } else if (a == "--hint-speed") {
      hintSpeed = true;
    } else {
      return Usage();
    }
  }
  MeshInput mesh;
  if (!paramsOnly) {
    std::vector<uint8_t> in;
    if (!ReadFile(argv[2], in)) {
      std::fprintf(stderr, "cannot read %s\n", argv[2]);
      return 2;
    }
    std::string err;
    if (!gms::physx::ParseMeshInput(in.data(), in.size(), mesh, err)) {
      std::fprintf(stderr, "%s: %s\n", argv[2], err.c_str());
      return 2;
    }
  }
  HMODULE dll = LoadLibraryExA(argv[1], nullptr, LOAD_WITH_ALTERED_SEARCH_PATH);
  if (!dll) {
    std::fprintf(stderr, "cannot load %s (error %lu)\n", argv[1], GetLastError());
    return 2;
  }
  const auto init = Proc<InitCookingFn>(dll, "NxInitCooking");
  const auto close = Proc<CloseCookingFn>(dll, "NxCloseCooking");
  const auto cook = Proc<CookTriangleMeshFn>(dll, "NxCookTriangleMesh");
  const auto getParams = Proc<GetCookingParamsFn>(dll, "NxGetCookingParams");
  const auto setParams = Proc<SetCookingParamsFn>(dll, "NxSetCookingParams");
  if (!init || !close || !cook || !getParams || !setParams) {
    std::fprintf(stderr, "%s lacks the PhysX 2.8 cooking exports\n", argv[1]);
    return 2;
  }
  PrintingOutput output;
  if (!init(nullptr, &output)) {
    std::fprintf(stderr, "NxInitCooking failed\n");
    return 1;
  }
  if (hintSpeed) {
    CookingParams28 p = *getParams();
    p.hintCollisionSpeed = 1;
    setParams(p);
  }
  const CookingParams28* p = getParams();
  std::printf("cooking params: platform %u, skinWidth %.4f, hintCollisionSpeed %u\n", p->targetPlatform,
              static_cast<double>(p->skinWidth), p->hintCollisionSpeed & 0xFF);
  if (paramsOnly) {
    close();
    return 0;
  }
  TriangleMeshDesc28 desc = gms::physx::FillDesc(mesh);
  desc.heightFieldVerticalAxis = axis;
  MemoryStream stream;
  const bool ok = cook(desc, stream);
  close();
  if (!ok || output.errors || !gms::physx::IsCookedMesh(stream.bytes)) {
    std::fprintf(stderr, "NxCookTriangleMesh failed (%s, %d error(s), %zu bytes)\n", ok ? "true" : "false",
                 output.errors, stream.bytes.size());
    return 1;
  }
  if (!WriteFile(argv[3], stream.bytes)) {
    std::fprintf(stderr, "cannot write %s\n", argv[3]);
    return 2;
  }
  std::printf("cooked %u vertices, %u triangles%s%s -> %s: %zu bytes, stored %u vertices, %u triangles\n",
              mesh.numVertices(), mesh.numTriangles(), mesh.materials.empty() ? "" : " with materials",
              axis == gms::physx::kNotHeightField ? "" : " (height-field axis set)", argv[3], stream.bytes.size(),
              gms::physx::CookedVertexCount(stream.bytes), gms::physx::CookedTriangleCount(stream.bytes));
  return 0;
}
