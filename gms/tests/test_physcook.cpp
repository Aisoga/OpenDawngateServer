// gms/core/physx/cook.h (MAPPHYS): the cooking tool's input format, the PhysX 2.8 descriptor it fills and the
// stream interface PhysXCooking.dll writes through, without the DLL (the real cook is checked against the
// shipped SM_Map_v5.bin: its triangles cooked again give the same cook up to the DLL's vertex cleaning).
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

#include "core/physx/cook.h"
#include "tests/check.h"

using namespace gms::physx;

namespace {

void PutU32(std::vector<uint8_t>& b, uint32_t v) {
  for (int i = 0; i < 4; ++i) b.push_back(static_cast<uint8_t>(v >> (8 * i)));
}
void PutF32(std::vector<uint8_t>& b, float f) {
  uint32_t v;
  std::memcpy(&v, &f, 4);
  PutU32(b, v);
}

// A unit square of two triangles, optionally with materials 7 and 9.
std::vector<uint8_t> Square(bool materials) {
  std::vector<uint8_t> b = {'D', 'G', 'M', 'I'};
  PutU32(b, 1);
  PutU32(b, 4);
  PutU32(b, 2);
  PutU32(b, materials ? 1 : 0);
  PutF32(b, 0.001f);
  const float v[12] = {0, 0, 0.09f, 1, 0, 0.09f, 1, 1, 0.09f, 0, 1, 0.09f};
  for (float f : v) PutF32(b, f);
  const uint32_t t[6] = {0, 1, 2, 0, 2, 3};
  for (uint32_t i : t) PutU32(b, i);
  if (materials) {
    b.push_back(7), b.push_back(0), b.push_back(9), b.push_back(0);
  }
  return b;
}

}  // namespace

GMS_TEST(PhysCookParsesInput) {
  for (bool mats : {false, true}) {
    const std::vector<uint8_t> b = Square(mats);
    MeshInput m;
    std::string err;
    CHECK(ParseMeshInput(b.data(), b.size(), m, err));
    CHECK_EQ(m.numVertices(), 4u);
    CHECK_EQ(m.numTriangles(), 2u);
    CHECK_EQ(m.triangles[5], 3u);
    CHECK(m.points[11] == 0.09f);
    CHECK(m.convexEdgeThreshold == 0.001f);
    CHECK_EQ(m.materials.size(), mats ? 2u : 0u);
    if (mats) CHECK_EQ(m.materials[1], 9);
  }
}

GMS_TEST(PhysCookRefusesBadInput) {
  MeshInput m;
  std::string err;
  std::vector<uint8_t> b = Square(false);
  b[0] = 'X';
  CHECK(!ParseMeshInput(b.data(), b.size(), m, err));
  b = Square(false);
  b.push_back(0);  // one byte too many
  CHECK(!ParseMeshInput(b.data(), b.size(), m, err));
  CHECK(err.find("does not match") != std::string::npos);
  b = Square(false);
  b.resize(b.size() - 1);  // one too few
  CHECK(!ParseMeshInput(b.data(), b.size(), m, err));
  b = Square(false);
  b[kMeshInputHeader + 48 + 20] = 4;  // the second triangle's last index: vertex 4 of 4
  CHECK(!ParseMeshInput(b.data(), b.size(), m, err));
  CHECK(err.find("beyond") != std::string::npos);
  b = Square(false);
  b[kMeshInputHeader + 48 + 16] = 0;  // the second triangle 0, 0, 3
  CHECK(!ParseMeshInput(b.data(), b.size(), m, err));
  CHECK(err.find("repeats") != std::string::npos);
  b = Square(false);
  b[4] = 2;  // version 2
  CHECK(!ParseMeshInput(b.data(), b.size(), m, err));
  b = Square(false);
  b[16] = 2;  // an unknown flag
  CHECK(!ParseMeshInput(b.data(), b.size(), m, err));
}

GMS_TEST(PhysCookFillsDesc) {
  const std::vector<uint8_t> b = Square(true);
  MeshInput m;
  std::string err;
  CHECK(ParseMeshInput(b.data(), b.size(), m, err));
  const TriangleMeshDesc28 d = FillDesc(m);
  CHECK_EQ(d.numVertices, 4u);
  CHECK_EQ(d.numTriangles, 2u);
  CHECK_EQ(d.pointStrideBytes, 12u);
  CHECK_EQ(d.triangleStrideBytes, 12u);
  CHECK(d.points == m.points.data());
  CHECK(d.triangles == m.triangles.data());
  CHECK_EQ(d.flags, 0u);
  CHECK_EQ(d.materialIndexStride, 2u);
  CHECK(d.materialIndices == m.materials.data());
  CHECK_EQ(d.heightFieldVerticalAxis, 0xFFu);
  CHECK(d.pmap == nullptr);
  CHECK(d.convexEdgeThreshold == 0.001f);
  // the DLL's validity check reads these dwords (PhysXCooking.dll 0x10004500)
  const uint32_t* w = reinterpret_cast<const uint32_t*>(&d);
  CHECK_EQ(w[0], 4u);
  CHECK_EQ(w[7], 2u);
  CHECK_EQ(w[9], 0xFFu);
  CHECK_EQ(sizeof(TriangleMeshDesc28), 0x34u);
  MeshInput none;
  CHECK(ParseMeshInput(Square(false).data(), Square(false).size(), none, err));
  const TriangleMeshDesc28 e = FillDesc(none);
  CHECK_EQ(e.materialIndexStride, 0u);
  CHECK(e.materialIndices == nullptr);
}

// The DLL calls the stream through its vtable by slot (storeByte at +0x1C); call the slots the same way.
GMS_TEST(PhysCookStreamSlots) {
  MemoryStream s;
  void** vt = *reinterpret_cast<void***>(&s);
  using StoreByte = Stream28&(__thiscall*)(void*, uint8_t);
  using StoreWord = Stream28&(__thiscall*)(void*, uint16_t);
  using StoreDword = Stream28&(__thiscall*)(void*, uint32_t);
  using StoreFloat = Stream28&(__thiscall*)(void*, float);
  using StoreBuffer = Stream28&(__thiscall*)(void*, const void*, uint32_t);
  using ReadDword = uint32_t(__thiscall*)(const void*);
  Stream28& r = reinterpret_cast<StoreByte>(vt[7])(&s, 'N');
  CHECK(&r == static_cast<Stream28*>(&s));
  reinterpret_cast<StoreWord>(vt[8])(&s, 0x5358);
  reinterpret_cast<StoreDword>(vt[9])(&s, 0x48534501u);
  reinterpret_cast<StoreFloat>(vt[10])(&s, 0.001f);
  reinterpret_cast<StoreBuffer>(vt[12])(&s, "M", 1);
  CHECK_EQ(s.bytes.size(), 12u);
  CHECK(std::memcmp(s.bytes.data(), "NXS\x01" "ESH", 7) == 0);
  CHECK_EQ(s.bytes[6], 0x48);
  CHECK_EQ(s.bytes[7], 0x6F);  // 0.001f = 0x3A83126F, as in SM_Map_v5.bin's header
  CHECK_EQ(s.bytes[11], 'M');
  CHECK_EQ(s.readByte(), 'N');
  CHECK_EQ(s.readWord(), 0x5358);
  CHECK_EQ(reinterpret_cast<ReadDword>(vt[3])(&s), 0x48534501u);
  CHECK(s.readFloat() == 0.001f);
  char m = 0;
  s.readBuffer(&m, 1);
  CHECK_EQ(m, 'M');
  CHECK(!s.overrun);
  CHECK_EQ(s.readDword(), 0u);
  CHECK(s.overrun);
}

GMS_TEST(PhysCookRecognisesOutput) {
  std::vector<uint8_t> b = {'N', 'X', 'S', 1, 'M', 'E', 'S', 'H', 1, 0, 0, 0};
  b.resize(36);
  b[0x1C] = 0xAE, b[0x1D] = 0x5E;  // SM_Map_v5.bin: 24,238 vertices
  b[0x20] = 0x40, b[0x21] = 0x7C;  // 31,808 triangles
  CHECK(IsCookedMesh(b));
  CHECK_EQ(CookedVertexCount(b), 24238u);
  CHECK_EQ(CookedTriangleCount(b), 31808u);
  b[3] = 0;  // a big-endian cook
  CHECK(!IsCookedMesh(b));
  CHECK_EQ(CookedVertexCount(b), 0u);
  b.resize(20);
  CHECK(!IsCookedMesh(b));
}
