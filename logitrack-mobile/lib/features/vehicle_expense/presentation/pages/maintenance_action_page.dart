import 'dart:typed_data';
import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';
import '../../../home/data/services/image_compression_service.dart';
import '../../../home/data/repositories/checkin_repository.dart'; // 📸 For stampOverlayAndCompressForEvidence
import '../../data/repositories/maintenance_repository.dart';

class MaintenanceActionPage extends StatefulWidget {
  final Map<String, dynamic> task;

  const MaintenanceActionPage({super.key, required this.task});

  @override
  State<MaintenanceActionPage> createState() => _MaintenanceActionPageState();
}

class _MaintenanceActionPageState extends State<MaintenanceActionPage> {
  final _amountController = TextEditingController();
  final _formKey = GlobalKey<FormState>();
  
  Uint8List? _invoicePhoto;
  bool _saving = false;

  @override
  void dispose() {
    _amountController.dispose();
    super.dispose();
  }

  Future<void> _pickInvoicePhoto() async {
    final picker = ImagePicker();
    final xfile = await picker.pickImage(source: ImageSource.camera, imageQuality: 85);
    if (xfile == null || !mounted) return;
    final bytes = await xfile.readAsBytes();
    if (!mounted) return;
    final compressed = await stampOverlayAndCompressForEvidence(bytes);
    if (!mounted) return;
    setState(() => _invoicePhoto = compressed);
  }

  Future<void> _handleCheckIn() async {
    setState(() => _saving = true);
    try {
      await MaintenanceRepository().checkInMaintenance(widget.task['id']);
      if (mounted) {
        Navigator.pop(context, true);
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('เช็คอินสถานีซ่อมสำเร็จเรียบร้อยครับ')),
        );
      }
    } catch (e) {
      if (mounted) {
        setState(() => _saving = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('เกิดข้อผิดพลาด: $e')),
        );
      }
    }
  }

  Future<void> _handleSubmitCompletion() async {
    if (!_formKey.currentState!.validate() || _invoicePhoto == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('กรุณากรอกข้อมูลและแนบรูปภาพใบเสร็จให้ครบถ้วนก่อนส่งงาน')),
      );
      return;
    }
    setState(() => _saving = true);
    try {
      final amount = double.parse(_amountController.text.trim());
      final uploadedUrl = await MaintenanceRepository().uploadMaintenancePhoto(
        taskId: widget.task['id'],
        photoType: 'invoice',
        imageBytes: _invoicePhoto!,
      );
      await MaintenanceRepository().submitMaintenanceCompletion(
        widget.task['id'],
        invoiceUrl: uploadedUrl,
        invoiceAmount: amount,
      );
      if (mounted) {
        Navigator.pop(context, true);
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('ส่งรายงานซ่อมให้ผู้ดูแลระบบตรวจสอบและปิดงานแล้วครับ')),
        );
      }
    } catch (e) {
      if (mounted) {
        setState(() => _saving = false);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('เกิดข้อผิดพลาดในการอัปโหลด: $e')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final status = widget.task['status'] as String? ?? '';
    final serviceType = widget.task['serviceType'] as String? ?? 'เช็คระยะตามรอบ';
    final notes = widget.task['notes'] as String? ?? '';
    final location = widget.task['locationName'] as String? ?? 'ไม่ระบุสถานที่';

    return Scaffold(
      appBar: AppBar(
        title: Text(serviceType),
      ),
      body: _saving
          ? const Center(child: CircularProgressIndicator())
          : SingleChildScrollView(
              padding: const EdgeInsets.all(16.0),
              child: Form(
                key: _formKey,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    _buildInfoSection(serviceType, notes, location, status),
                    const SizedBox(height: 24),
                    
                    if (status == 'Scheduled') ...[
                       const Text(
                        'ขั้นตอนที่ 1: การรับรถเข้าศูนย์',
                        style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
                      ),
                      const SizedBox(height: 12),
                      ElevatedButton.icon(
                        style: ElevatedButton.styleFrom(
                          backgroundColor: Colors.blue,
                          padding: const EdgeInsets.symmetric(vertical: 16),
                        ),
                        onPressed: _handleCheckIn,
                        icon: const Icon(Icons.location_on),
                        label: const Text('เช็คอินนำรถเข้าอู่ซ่อมบำรุง', style: TextStyle(fontSize: 16)),
                      ),
                    ],

                    if (status == 'In-Progress') ...[
                      const Text(
                        'ขั้นตอนที่ 2: ดำเนินงานซ่อมบำรุงเสร็จสิ้น',
                        style: TextStyle(fontWeight: FontWeight.bold, fontSize: 16),
                      ),
                      const SizedBox(height: 16),
                      TextFormField(
                        controller: _amountController,
                        keyboardType: TextInputType.number,
                        decoration: const InputDecoration(
                          labelText: 'ยอดเงินค่าใช้จ่ายตามใบเสร็จ (บาท)',
                          hintText: 'กรอกตัวเลขยอดรวมค่าใช้จ่าย',
                        ),
                        validator: (value) {
                          if (value == null || value.isEmpty) return 'กรุณากรอกยอดเงิน';
                          if (double.tryParse(value) == null) return 'กรุณากรอกตัวเลขที่ถูกต้อง';
                          return null;
                        },
                      ),
                      const SizedBox(height: 16),
                      const Text('แนบรูปถ่าย หรือ บิลใบเสร็จ:'),
                      const SizedBox(height: 8),
                      InkWell(
                        onTap: _pickInvoicePhoto,
                        child: Container(
                          height: 200,
                          decoration: BoxDecoration(
                            border: Border.all(color: Colors.grey),
                            borderRadius: BorderRadius.circular(8),
                            color: Colors.grey[100],
                          ),
                          child: _invoicePhoto != null
                              ? Image.memory(_invoicePhoto!, fit: BoxFit.cover)
                              : Column(
                                  mainAxisAlignment: MainAxisAlignment.center,
                                  children: [
                                    const Icon(Icons.camera_alt, size: 40, color: Colors.blue),
                                    const SizedBox(height: 8),
                                    Text('แตะเพื่อถ่ายรูปใบเสร็จซ่อม', style: TextStyle(color: Colors.grey[700])),
                                  ],
                                ),
                        ),
                      ),
                      const SizedBox(height: 24),
                      ElevatedButton(
                        style: ElevatedButton.styleFrom(
                          backgroundColor: Colors.green,
                          padding: const EdgeInsets.symmetric(vertical: 16),
                        ),
                        onPressed: _handleSubmitCompletion,
                        child: const Text('แจ้งซ่อมเสร็จ & ส่งให้ผู้ตรวจ', style: TextStyle(fontSize: 16)),
                      ),
                    ],
                  ],
                ),
              ),
            ),
    );
  }

  Widget _buildInfoSection(String serviceType, String notes, String location, String status) {
    Color statusColor = Colors.orange;
    if (status == 'In-Progress') statusColor = Colors.blue;
    if (status == 'Scheduled') statusColor = Colors.teal;

    return Card(
      elevation: 3,
      child: Padding(
        padding: const EdgeInsets.all(16.0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                const Text('สถานะงานซ่อมเเจ้งเตือน:', style: TextStyle(fontWeight: FontWeight.bold)),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                  decoration: BoxDecoration(
                    color: statusColor.withOpacity(0.15),
                    borderRadius: BorderRadius.circular(8),
                    border: Border.all(color: statusColor),
                  ),
                  child: Text(status, style: TextStyle(color: statusColor, fontWeight: FontWeight.bold, fontSize: 12)),
                ),
              ],
            ),
            const Divider(),
            const SizedBox(height: 8),
            Text('ประเภทซ่อม: $serviceType', style: const TextStyle(fontWeight: FontWeight.bold)),
            const SizedBox(height: 8),
            Text('สถานที่นัดหมาย: $location'),
            const SizedBox(height: 8),
            Text('รายละเอียดเพิ่ม: $notes', style: const TextStyle(color: Colors.grey)),
          ],
        ),
      ),
    );
  }
}
