export const USER_ROLES = {
  DRIVER: "driver",
  OPERATION_ADMIN: "operation_admin",
  CUSTOMER: "customer",
  SUPER_ADMIN: "super_admin",
} as const;

export const ASSIGNMENT_TYPES = {
  FIRST_MILES: "first_miles",  // มอบหมายโดย Admin/Customer
  LINE_HAULS: "line_hauls",     // Driver มอบหมายให้ตัวเอง
} as const;

export const ASSIGNMENT_STATUS = {
  PENDING: "pending",
  ACCEPTED: "accepted",
  STARTED: "started",
  CHECKED_IN: "checked_in",
  PICKED_UP: "picked_up",
  DEPARTED: "departed",
  DELAYED: "delayed",
  ARRIVED: "arrived",
  DELIVERED: "delivered",
  COMPLETED: "completed",
  CANCELLED: "cancelled",
} as const;

// Driver workflow steps
export const DRIVER_WORKFLOW_STEPS = [
  { status: "pending", label: "รอรับงาน", icon: "clock" },
  { status: "accepted", label: "รับงาน", icon: "check" },
  { status: "started", label: "เริ่มงาน", icon: "play" },
  { status: "checked_in", label: "เช็คอิน", icon: "map-pin" },
  { status: "picked_up", label: "บันทึกรับงาน", icon: "package" },
  { status: "departed", label: "ออกเดือนทาง", icon: "truck" },
  { status: "arrived", label: "ถึงที่หมาย", icon: "map-pin" },
  { status: "delivered", label: "บันทึกจัดส่งสำเร็จ", icon: "check-circle" },
] as const;

export const THAILAND_PROVINCES = [
  "กรุงเทพมหานคร", "กระบี่", "กาญจนบุรี", "กาฬสินธุ์", "กำแพงเพชร", "ขอนแก่น", "จันทบุรี", "ฉะเชิงเทรา", 
  "ชลบุรี", "ชัยนาท", "ชัยภูมิ", "ชุมพร", "เชียงราย", "เชียงใหม่", "ตรัง", "ตราด", "ตาก", "นครนายก", 
  "นครปฐม", "นครพนม", "นครราชสีมา", "นครศรีธรรมราช", "นครสวรรค์", "นนทบุรี", "นราธิวาส", "น่าน", 
  "บึงกาฬ", "บุรีรัมย์", "ปทุมธานี", "ประจวบคีรีขันธ์", "ปราจีนบุรี", "ปัตตานี", "พระนครศรีอยุธยา", 
  "พะเยา", "พังงา", "พัทลุง", "พิจิตร", "พิษณุโลก", "เพชรบุรี", "เพชรบูรณ์", "แพร่", "ภูเก็ต", 
  "มหาสารคาม", "มุกดาหาร", "แม่ฮ่องสอน", "ยโสธร", "ยะลา", "ร้อยเอ็ด", "ระนอง", "ระยอง", "ราชบุรี", 
  "ลพบุรี", "ลำปาง", "ลำพูน", "เลย", "ศรีสะเกษ", "สกลนคร", "สงขลา", "สตูล", "สมุทรปราการ", 
  "สมุทรสงคราม", "สมุทรสาคร", "สระแก้ว", "สระบุรี", "สิงห์บุรี", "สุโขทัย", "สุพรรณบุรี", 
  "สุราษฎร์ธานี", "สุรินทร์", "หนองคาย", "หนองบัวลำภู", "อ่างทอง", "อำนาจเจริญ", "อุดรธานี", 
  "อุตรดิตถ์", "อุทัยธานี", "อุบลราชธานี"
] as const;
