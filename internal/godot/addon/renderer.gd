extends RefCounted

# A software scene renderer. No GPU, viewport readback, browser or graphical
# window participates in this target. Native Godot simulation owns the scene.
const MAX_NODES := 1024
const MAX_POINTS := 256
const MAX_TEXTURE_EDGE := 1024

var image: Image
var viewport: Vector2
var scale_to_pixels: Transform2D
var textures: Dictionary = {}

static func validate(root: Node, context: String = "scene") -> PackedStringArray:
	var failures: PackedStringArray = []
	var pending: Array[Node] = [root]
	var count := 0
	while not pending.is_empty():
		var node := pending.pop_back() as Node
		count += 1
		if count > MAX_NODES:
			failures.append(context + ": scene exceeds 1024 nodes")
			break
		var where := context + "/" + str(root.get_path_to(node))
		if node is Node3D or node is Viewport or node is CanvasLayer or node is Camera2D or node is AudioStreamPlayer:
			failures.append(where + ": unsupported " + node.get_class())
		elif node is CanvasItem:
			if not (node.get_class() == "Node2D" or node is CollisionObject2D or node is CollisionShape2D or node is CollisionPolygon2D or node is Sprite2D or node is Polygon2D or node is Line2D or node is ColorRect):
				failures.append(where + ": unsupported visual node " + node.get_class())
			if node.material != null or node.clip_children != CanvasItem.CLIP_CHILDREN_DISABLED:
				failures.append(where + ": materials and canvas clipping are unsupported")
			if node is Node2D and node.y_sort_enabled:
				failures.append(where + ": y sorting is unsupported; use z_index")
			if node.get_script() != null:
				for method in node.get_script().get_script_method_list():
					if method.name == "_draw":
						failures.append(where + ": custom _draw requires a terminal renderer adapter")
		if node is Sprite2D:
			if node.texture == null:
				failures.append(where + ": Sprite2D requires a texture")
			elif node.texture.get_width() > MAX_TEXTURE_EDGE or node.texture.get_height() > MAX_TEXTURE_EDGE:
				failures.append(where + ": texture exceeds 1024 pixels per edge")
		elif node is Polygon2D:
			if node.texture != null or not node.vertex_colors.is_empty() or not node.polygons.is_empty() or not node.skeleton.is_empty() or node.invert_enabled:
				failures.append(where + ": only solid unskinned polygons are supported")
			if node.polygon.size() > MAX_POINTS:
				failures.append(where + ": polygon exceeds 256 points")
		elif node is Line2D:
			if node.texture != null or node.gradient != null or node.width_curve != null:
				failures.append(where + ": only solid constant-width lines are supported")
			if node.points.size() > MAX_POINTS:
				failures.append(where + ": line exceeds 256 points")
		pending.append_array(node.get_children())
	return failures

func render(root: Node, width: int, height: int, world_size: Vector2) -> Image:
	image = Image.create(width, height, false, Image.FORMAT_RGB8)
	image.fill(ProjectSettings.get_setting("rendering/environment/defaults/default_clear_color", Color.BLACK))
	viewport = world_size
	scale_to_pixels = Transform2D(Vector2(width / world_size.x, 0), Vector2(0, height / world_size.y), Vector2.ZERO)
	var items: Array[Dictionary] = []
	_collect(root, items, 0, Color.WHITE)
	items.sort_custom(func(a: Dictionary, b: Dictionary) -> bool: return a.z < b.z)
	for item in items:
		_draw_item(item.node, item.color)
	return image

func _collect(node: Node, items: Array[Dictionary], parent_z: int, inherited: Color) -> void:
	var z := parent_z
	var tint := inherited
	if node is CanvasItem:
		if not node.is_visible_in_tree():
			return
		z = parent_z + node.z_index if node.z_as_relative else node.z_index
		tint *= node.modulate
		if node is Sprite2D or node is Polygon2D or node is Line2D or node is ColorRect:
			items.append({"node": node, "z": z, "color": tint * node.self_modulate})
	for child in node.get_children():
		_collect(child, items, z, tint)

func _blend(x: int, y: int, color: Color) -> void:
	if color.a > 0.0:
		image.set_pixel(x, y, image.get_pixel(x, y).lerp(Color(color.r, color.g, color.b), color.a))

func _draw_item(node: CanvasItem, tint: Color) -> void:
	var transform := scale_to_pixels * node.get_global_transform()
	if is_zero_approx(transform.determinant()):
		return
	var inverse := transform.affine_inverse()
	var polygon: PackedVector2Array = []
	var rect := Rect2()
	var texture: Image
	var source := Rect2()
	if node is Sprite2D:
		rect = node.get_rect()
		var key: int = node.texture.get_instance_id()
		if not textures.has(key):
			textures[key] = node.texture.get_image()
		texture = textures[key]
		if texture == null or texture.is_empty():
			return
		source = node.region_rect if node.region_enabled else Rect2(0, 0, texture.get_width(), texture.get_height())
		source.size /= Vector2(node.hframes, node.vframes)
		source.position += Vector2(node.frame_coords) * source.size
	elif node is ColorRect:
		rect = Rect2(Vector2.ZERO, node.size)
	elif node is Polygon2D:
		for point in node.polygon:
			polygon.append(point + node.offset)
	elif node is Line2D:
		polygon = node.points
	if polygon.is_empty():
		polygon = PackedVector2Array([rect.position, rect.position + Vector2(rect.size.x, 0), rect.end, rect.position + Vector2(0, rect.size.y)])
	if polygon.is_empty():
		return
	var bounds := Rect2(transform * polygon[0], Vector2.ZERO)
	for point in polygon:
		bounds = bounds.expand(transform * point)
	if node is Line2D:
		bounds = bounds.grow(node.width * maxf(transform.x.length(), transform.y.length()))
	var x0 := clampi(int(floor(bounds.position.x)), 0, image.get_width())
	var y0 := clampi(int(floor(bounds.position.y)), 0, image.get_height())
	var x1 := clampi(int(ceil(bounds.end.x)), 0, image.get_width())
	var y1 := clampi(int(ceil(bounds.end.y)), 0, image.get_height())
	for y in range(y0, y1):
		for x in range(x0, x1):
			var local: Vector2 = inverse * Vector2(x + 0.5, y + 0.5)
			if node is Sprite2D:
				if not rect.has_point(local):
					continue
				var uv := (local - rect.position) / rect.size
				if node.flip_h:
					uv.x = 1.0 - uv.x
				if node.flip_v:
					uv.y = 1.0 - uv.y
				var sample := source.position + uv * source.size
				if sample.x >= 0 and sample.y >= 0 and sample.x < texture.get_width() and sample.y < texture.get_height():
					_blend(x, y, texture.get_pixel(int(sample.x), int(sample.y)) * tint)
			elif node is Line2D:
				var points: PackedVector2Array = node.points
				for index in range(points.size() if node.closed else points.size() - 1):
					if local.distance_to(Geometry2D.get_closest_point_to_segment(local, points[index], points[(index + 1) % points.size()])) <= node.width / 2.0:
						_blend(x, y, node.default_color * tint)
						break
			elif Geometry2D.is_point_in_polygon(local, polygon):
				_blend(x, y, node.color * tint)
