// gms/core/physx (MAPPHYS): what the cooking tool gms/physcook needs from PhysX 2.8.4 without the PhysX SDK headers,
// header-only and without any game address.
//
//   MeshInput / ParseMeshInput   the tool's input file ("DGMI" v1, written by the level-mesh builder): vertices,
//                                triangles, optional per-triangle material indices, the convex-edge threshold
//   TriangleMeshDesc28           PhysX 2.8's NxTriangleMeshDesc (NxSimpleTriangleMesh + its own fields), 13 dwords,
//                                as PhysXCooking.dll 2.8.4.4 reads it (its NxCookTriangleMesh copies 0x34 bytes and
//                                checks +0x00 >= 3 vertices, +0x14 triangles, +0x1C material stride >= 2 when +0x20
//                                is set); FillDesc points one at a MeshInput
//   Stream28 / MemoryStream      PhysX 2.8's NxStream interface (the cooker writes through slots 7-12: storeByte at
//                                vtable +0x1C, seen in the DLL's header writer) and a growing in-memory stream
//   OutputStream28               NxUserOutputStream: reportError (slot 0, 4 arguments: the DLL calls it so),
//                                reportAssertViolation, print
//   IsCookedMesh                 the output's header: "NXS", 1 (little-endian), "MESH", version 1
//
// Only meaningful in a 32-bit build (the DLL is x86): the layout asserts need 4-byte pointers.
#pragma once

#include <cstddef>
#include <cstdint>
#include <cstring>
#include <string>
#include <vector>

namespace gms::physx {

// ---- the input file ---------------------------------------------------------------------------------------------
// "DGMI", u32 version 1, u32 numVertices, u32 numTriangles, u32 flags (bit 0: material indices follow),
// f32 convexEdgeThreshold, then numVertices x 3 f32 (x, y, z), numTriangles x 3 u32 (vertex indices),
// and with bit 0 numTriangles x u16 material indices. Little-endian, no padding.
constexpr uint32_t kMeshInputVersion = 1;
constexpr uint32_t kMeshInputHasMaterials = 1;
constexpr size_t kMeshInputHeader = 24;

struct MeshInput {
  std::vector<float> points;       // 3 per vertex
  std::vector<uint32_t> triangles;  // 3 per triangle
  std::vector<uint16_t> materials;  // empty, or 1 per triangle
  float convexEdgeThreshold = 0.001f;
  uint32_t numVertices() const { return static_cast<uint32_t>(points.size() / 3); }
  uint32_t numTriangles() const { return static_cast<uint32_t>(triangles.size() / 3); }
};

inline uint32_t ReadU32(const uint8_t* p) {
  return static_cast<uint32_t>(p[0]) | static_cast<uint32_t>(p[1]) << 8 | static_cast<uint32_t>(p[2]) << 16 |
         static_cast<uint32_t>(p[3]) << 24;
}

// Parses and checks an input file: the sizes must match exactly, every index must name a vertex, and no triangle may
// repeat a vertex. On failure returns false with the reason in err.
inline bool ParseMeshInput(const uint8_t* data, size_t size, MeshInput& out, std::string& err) {
  if (size < kMeshInputHeader || std::memcmp(data, "DGMI", 4) != 0) {
    err = "not a DGMI mesh input (no magic)";
    return false;
  }
  const uint32_t version = ReadU32(data + 4), nv = ReadU32(data + 8), nt = ReadU32(data + 12);
  const uint32_t flags = ReadU32(data + 16);
  if (version != kMeshInputVersion) {
    err = "unsupported DGMI version " + std::to_string(version);
    return false;
  }
  if (flags & ~kMeshInputHasMaterials) {
    err = "unknown DGMI flags";
    return false;
  }
  if (nv < 3 || nt < 1 || nv > (1u << 24) || nt > (1u << 24)) {
    err = "vertex or triangle count out of range";
    return false;
  }
  const bool mats = (flags & kMeshInputHasMaterials) != 0;
  const uint64_t want = kMeshInputHeader + uint64_t{nv} * 12 + uint64_t{nt} * 12 + (mats ? uint64_t{nt} * 2 : 0);
  if (want != size) {
    err = "size " + std::to_string(size) + " does not match the counts (" + std::to_string(want) + ")";
    return false;
  }
  std::memcpy(&out.convexEdgeThreshold, data + 20, 4);
  const uint8_t* p = data + kMeshInputHeader;
  out.points.resize(size_t{nv} * 3);
  std::memcpy(out.points.data(), p, size_t{nv} * 12);
  p += size_t{nv} * 12;
  out.triangles.resize(size_t{nt} * 3);
  std::memcpy(out.triangles.data(), p, size_t{nt} * 12);
  p += size_t{nt} * 12;
  out.materials.clear();
  if (mats) {
    out.materials.resize(nt);
    std::memcpy(out.materials.data(), p, size_t{nt} * 2);
  }
  for (uint32_t t = 0; t < nt; ++t) {
    const uint32_t a = out.triangles[3 * t], b = out.triangles[3 * t + 1], c = out.triangles[3 * t + 2];
    if (a >= nv || b >= nv || c >= nv) {
      err = "triangle " + std::to_string(t) + " names a vertex beyond " + std::to_string(nv);
      return false;
    }
    if (a == b || b == c || a == c) {
      err = "triangle " + std::to_string(t) + " repeats a vertex";
      return false;
    }
  }
  return true;
}

// ---- NxTriangleMeshDesc (2.8) ----------------------------------------------------------------------------------
constexpr uint32_t kNotHeightField = 0xFF;  // NxHeightFieldAxis NX_NOT_HEIGHTFIELD

struct TriangleMeshDesc28 {
  // NxSimpleTriangleMesh
  uint32_t numVertices;
  uint32_t numTriangles;
  uint32_t pointStrideBytes;
  uint32_t triangleStrideBytes;
  const void* points;
  const void* triangles;
  uint32_t flags;  // NxMeshFlags: 1 flip normals, 2 16-bit indices, 4 hardware mesh
  // NxTriangleMeshDesc
  uint32_t materialIndexStride;
  const void* materialIndices;
  uint32_t heightFieldVerticalAxis;
  float heightFieldVerticalExtent;
  void* pmap;
  float convexEdgeThreshold;
};
#if defined(_WIN32) && !defined(_WIN64)
static_assert(sizeof(TriangleMeshDesc28) == 0x34, "PhysXCooking.dll copies 13 dwords");
static_assert(offsetof(TriangleMeshDesc28, triangles) == 0x14, "the DLL's check reads +0x14");
static_assert(offsetof(TriangleMeshDesc28, materialIndexStride) == 0x1C, "the DLL's check reads +0x1C");
static_assert(offsetof(TriangleMeshDesc28, materialIndices) == 0x20, "the DLL's check reads +0x20");
static_assert(offsetof(TriangleMeshDesc28, heightFieldVerticalAxis) == 0x24, "the cooker reads +0x24");
static_assert(offsetof(TriangleMeshDesc28, convexEdgeThreshold) == 0x30, "the cooker reads +0x30");
#endif

// The descriptor for a parsed input: 32-bit indices, no height field, no pmap, the input's threshold; the shipped
// level meshes' header says the same (flags 0x13 = materials, face remap, 16-bit stored indices; 0.001; 0xFF; 0).
inline TriangleMeshDesc28 FillDesc(const MeshInput& in) {
  TriangleMeshDesc28 d{};
  d.numVertices = in.numVertices();
  d.numTriangles = in.numTriangles();
  d.pointStrideBytes = 12;
  d.triangleStrideBytes = 12;
  d.points = in.points.data();
  d.triangles = in.triangles.data();
  d.flags = 0;
  d.materialIndexStride = in.materials.empty() ? 0 : 2;
  d.materialIndices = in.materials.empty() ? nullptr : in.materials.data();
  d.heightFieldVerticalAxis = kNotHeightField;
  d.heightFieldVerticalExtent = 0.0f;
  d.pmap = nullptr;
  d.convexEdgeThreshold = in.convexEdgeThreshold;
  return d;
}

// ---- NxStream (2.8) ---------------------------------------------------------------------------------------------
class Stream28 {
 public:
  virtual ~Stream28() {}
  virtual uint8_t readByte() const = 0;
  virtual uint16_t readWord() const = 0;
  virtual uint32_t readDword() const = 0;
  virtual float readFloat() const = 0;
  virtual double readDouble() const = 0;
  virtual void readBuffer(void* buffer, uint32_t size) const = 0;
  virtual Stream28& storeByte(uint8_t b) = 0;
  virtual Stream28& storeWord(uint16_t w) = 0;
  virtual Stream28& storeDword(uint32_t d) = 0;
  virtual Stream28& storeFloat(float f) = 0;
  virtual Stream28& storeDouble(double f) = 0;
  virtual Stream28& storeBuffer(const void* buffer, uint32_t size) = 0;
};

// Writes append to bytes; reads take from the read position (past the end they give zeros and set overrun).
class MemoryStream : public Stream28 {
 public:
  std::vector<uint8_t> bytes;
  mutable size_t readPos = 0;
  mutable bool overrun = false;

  uint8_t readByte() const override {
    uint8_t v = 0;
    Take(&v, 1);
    return v;
  }
  uint16_t readWord() const override {
    uint16_t v = 0;
    Take(&v, 2);
    return v;
  }
  uint32_t readDword() const override {
    uint32_t v = 0;
    Take(&v, 4);
    return v;
  }
  float readFloat() const override {
    float v = 0;
    Take(&v, 4);
    return v;
  }
  double readDouble() const override {
    double v = 0;
    Take(&v, 8);
    return v;
  }
  void readBuffer(void* buffer, uint32_t size) const override { Take(buffer, size); }
  Stream28& storeByte(uint8_t b) override { return Put(&b, 1); }
  Stream28& storeWord(uint16_t w) override { return Put(&w, 2); }
  Stream28& storeDword(uint32_t d) override { return Put(&d, 4); }
  Stream28& storeFloat(float f) override { return Put(&f, 4); }
  Stream28& storeDouble(double f) override { return Put(&f, 8); }
  Stream28& storeBuffer(const void* buffer, uint32_t size) override { return Put(buffer, size); }

 private:
  Stream28& Put(const void* p, size_t n) {
    const uint8_t* b = static_cast<const uint8_t*>(p);
    bytes.insert(bytes.end(), b, b + n);
    return *this;
  }
  void Take(void* p, size_t n) const {
    std::memset(p, 0, n);
    if (readPos + n > bytes.size()) {
      overrun = true;
      return;
    }
    std::memcpy(p, bytes.data() + readPos, n);
    readPos += n;
  }
};

// ---- NxUserOutputStream (2.8) ----------------------------------------------------------------------------------
class OutputStream28 {
 public:
  virtual void reportError(int code, const char* message, const char* file, int line) = 0;
  virtual int reportAssertViolation(const char* message, const char* file, int line) = 0;  // 0 = continue
  virtual void print(const char* message) = 0;
};

// ---- the output -------------------------------------------------------------------------------------------------
// "NXS" + 1 (the endianness byte of a little-endian cook) + "MESH" + u32 version 1.
inline bool IsCookedMesh(const std::vector<uint8_t>& b) {
  static const uint8_t head[12] = {'N', 'X', 'S', 1, 'M', 'E', 'S', 'H', 1, 0, 0, 0};
  return b.size() >= 36 && std::memcmp(b.data(), head, sizeof head) == 0;
}

// The stored counts of a cooked mesh (+0x1C vertices, +0x20 triangles); 0 when it isn't one.
inline uint32_t CookedVertexCount(const std::vector<uint8_t>& b) { return IsCookedMesh(b) ? ReadU32(&b[0x1C]) : 0; }
inline uint32_t CookedTriangleCount(const std::vector<uint8_t>& b) { return IsCookedMesh(b) ? ReadU32(&b[0x20]) : 0; }

}  // namespace gms::physx
